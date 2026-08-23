package collector

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/danweinerdev/mb8611-poller/internal/hnap/hnaptest"
)

func TestMakeCollectFuncFixtures(t *testing.T) {
	mock := fixtureClient()
	collector := NewCollector(mock, Config{Host: "collector-host", ModemTag: "modem.lan"}, slog.Default())
	metrics, err := MakeCollectFunc(collector)(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	for _, metric := range metrics {
		counts[metric.Measurement]++
		if metric.Tags["host"] != "collector-host" || metric.Tags["modem"] != "modem.lan" {
			t.Fatalf("metric %q base tags = %v", metric.Measurement, metric.Tags)
		}
		if metric.Measurement == "docsis_event" && metric.Tags["doc_id"] == "" {
			t.Fatalf("event metric missing doc_id: %+v", metric)
		}
	}

	if got, want := counts["docsis_downstream"], 34; got != want {
		t.Errorf("downstream metric count = %d, want %d", got, want)
	}
	if got, want := counts["docsis_upstream"], 5; got != want {
		t.Errorf("upstream metric count = %d, want %d", got, want)
	}
	if counts["docsis_modem"] != 1 {
		t.Errorf("modem metric count = %d, want 1", counts["docsis_modem"])
	}
	if counts["docsis_event"] == 0 {
		t.Error("event metric count = 0, want at least one")
	}
	if got, want := len(metrics), counts["docsis_downstream"]+counts["docsis_upstream"]+counts["docsis_modem"]+counts["docsis_event"]; got != want {
		t.Errorf("metric count = %d, want %d", got, want)
	}

	var downstream, modemFound bool
	for _, metric := range metrics {
		switch metric.Measurement {
		case "docsis_downstream":
			if metric.Tags["channel"] == "1" {
				downstream = metric.Fields["frequency_mhz"] == 237.0 && metric.Fields["power_dbmv"] == 3.0 && metric.Fields["corrected"] == uint64(411)
			}
		case "docsis_modem":
			modemFound = metric.Fields["uptime_seconds"] == (1083*time.Hour+9*time.Minute+15*time.Second).Seconds() && metric.Fields["ds_freq_mhz"] == 237.0
		}
	}
	if !downstream {
		t.Error("downstream metric did not contain the expected numeric fields")
	}
	if !modemFound {
		t.Error("modem metric did not contain the expected numeric fields")
	}

	wantActions := []string{downstreamAction, upstreamAction, connectionAction, softwareAction, startupAction, logAction}
	if got := mock.LastMultiArgs; len(got) != len(wantActions) {
		t.Fatalf("Multi actions = %v, want %v", got, wantActions)
	} else {
		for i, want := range wantActions {
			if got[i] != want {
				t.Errorf("Multi action %d = %q, want %q", i, got[i], want)
			}
		}
	}
}

func TestNewCollectorDefaultsModemTag(t *testing.T) {
	collector := NewCollector(hnaptest.NewMockClient(), Config{Host: "192.168.100.1"}, nil)
	if got, want := collector.cfg.ModemTag, "192.168.100.1"; got != want {
		t.Errorf("default modem tag = %q, want %q", got, want)
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if collector.clock == nil {
		t.Error("default clock is nil")
	}
}

func TestMakeCollectFuncLockoutCooldownSkipsHNAPAndRecovers(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	backoff := 6 * time.Minute
	mock := fixtureClient()
	mock.LockedOut = true
	collector := NewCollector(mock, Config{LockoutBackoff: backoff}, slog.Default())
	setCollectorClock(collector, func() time.Time { return now })
	collect := MakeCollectFunc(collector)

	metrics, err := collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if metrics != nil {
		t.Errorf("metrics = %v, want nil", metrics)
	}
	deadline := lockoutDeadline(collector)
	if got, want := deadline, now.Add(backoff); !got.Equal(want) {
		t.Errorf("lockout deadline = %v, want %v", got, want)
	}
	assertNoHNAPTraffic(t, mock)

	now = now.Add(backoff / 2)
	metrics, err = collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if metrics != nil {
		t.Errorf("cooldown metrics = %v, want nil", metrics)
	}
	if got := lockoutDeadline(collector); !got.Equal(deadline) {
		t.Errorf("lockout deadline was extended to %v, want original %v", got, deadline)
	}
	assertNoHNAPTraffic(t, mock)

	now = deadline.Add(time.Nanosecond)
	mock.LockedOut = false
	metrics, err = collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) == 0 {
		t.Error("post-cooldown metrics are empty")
	}
	if got := lockoutDeadline(collector); !got.IsZero() {
		t.Errorf("lockout deadline after successful recovery = %v, want zero", got)
	}
	if got, want := mock.LoginCount, 1; got != want {
		t.Errorf("post-cooldown login count = %d, want %d", got, want)
	}
	for _, action := range []string{downstreamAction, upstreamAction, connectionAction, softwareAction, startupAction, logAction} {
		if got, want := mock.CallCounts[action], 1; got != want {
			t.Errorf("post-cooldown call count for %q = %d, want %d", action, got, want)
		}
	}
}

func assertNoHNAPTraffic(t *testing.T, mock *hnaptest.MockClient) {
	t.Helper()
	if mock.LoginCount != 0 {
		t.Errorf("login count = %d, want 0 during cooldown", mock.LoginCount)
	}
	if len(mock.CallCounts) != 0 {
		t.Errorf("HNAP call counts = %v, want no calls during cooldown", mock.CallCounts)
	}
}

func setCollectorClock(collector *Collector, clock func() time.Time) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.clock = clock
}

func lockoutDeadline(collector *Collector) time.Time {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return collector.lockoutUntil
}

func fixtureClient() *hnaptest.MockClient {
	return hnaptest.NewMockClient().
		WithFixture(downstreamAction, "GetMotoStatusDownstreamChannelInfo.json").
		WithFixture(upstreamAction, "GetMotoStatusUpstreamChannelInfo.json").
		WithFixture(connectionAction, "GetMotoStatusConnectionInfo.json").
		WithFixture(softwareAction, "GetMotoStatusSoftware.json").
		WithFixture(startupAction, "GetMotoStatusStartupSequence.json").
		WithFixture(logAction, "GetMotoStatusLog.json")
}
