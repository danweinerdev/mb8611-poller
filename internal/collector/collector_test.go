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
}

func TestMakeCollectFuncLockedOutSetsDeadline(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	collector := NewCollector(hnaptest.NewMockClient(), Config{LockoutBackoff: 6 * time.Minute}, slog.Default())
	collector.clock = func() time.Time { return now }
	collector.client.(*hnaptest.MockClient).LockedOut = true

	metrics, err := MakeCollectFunc(collector)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if metrics != nil {
		t.Errorf("metrics = %v, want nil", metrics)
	}
	if got, want := collector.lockoutUntil, now.Add(6*time.Minute); !got.Equal(want) {
		t.Errorf("lockout deadline = %v, want %v", got, want)
	}
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
