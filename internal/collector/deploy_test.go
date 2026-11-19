package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	monitor "github.com/danweinerdev/go-monitor"
	"github.com/danweinerdev/mb8611-poller/internal/hnap"
	"github.com/danweinerdev/mb8611-poller/internal/hnap/hnaptest"
)

func eventMetrics(metrics []*monitor.Metric) []*monitor.Metric {
	var events []*monitor.Metric
	for _, metric := range metrics {
		if metric.Measurement == "docsis_event" {
			events = append(events, metric)
		}
	}
	return events
}

func onlyMetric(t *testing.T, metrics []*monitor.Metric, measurement string) *monitor.Metric {
	t.Helper()
	var found *monitor.Metric
	for _, metric := range metrics {
		if metric.Measurement != measurement {
			continue
		}
		if found != nil {
			t.Fatalf("more than one %s metric", measurement)
		}
		found = metric
	}
	if found == nil {
		t.Fatalf("no %s metric in %d metrics", measurement, len(metrics))
	}
	return found
}

func logResponse(entries ...string) json.RawMessage {
	list, _ := json.Marshal(strings.Join(entries, "}-{"))
	return json.RawMessage(`{"MotoStatusLogList":` + string(list) + `,"GetMotoStatusLogResult":"OK"}`)
}

func TestEventMetricsUseModemEventTime(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	pollTime := time.Date(2026, time.August, 23, 5, 30, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		loc    *time.Location
		offset time.Duration
	}{
		{name: "default UTC", loc: nil},
		{name: "America/Chicago", loc: chicago, offset: 5 * time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			collector := NewCollector(fixtureClient(), Config{EventLocation: test.loc}, slog.Default())
			setCollectorClock(collector, func() time.Time { return pollTime })
			metrics, err := MakeCollectFunc(collector)(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			events := eventMetrics(metrics)
			if len(events) == 0 {
				t.Fatal("no event metrics")
			}
			for _, event := range events {
				rawDate, rawTime := event.Fields["raw_date"].(string), event.Fields["raw_time"].(string)
				if strings.ContainsAny(rawDate, "\n\r") || rawDate != strings.TrimSpace(rawDate) {
					t.Errorf("raw_date keeps whitespace: %q", rawDate)
				}
				wallClock, err := time.Parse("Mon Jan 2 2006 15:04:05", rawDate+" "+rawTime)
				if err != nil {
					t.Fatal(err)
				}
				if want := wallClock.Add(test.offset); !event.Timestamp.Equal(want) {
					t.Errorf("event %q at %s %s timestamp = %v, want %v", event.Tags["doc_id"], rawDate, rawTime, event.Timestamp, want)
				}
			}
			first := events[0].Timestamp
			if want := time.Date(2026, time.August, 20, 11, 57, 22, 0, time.UTC).Add(test.offset); !first.Equal(want) {
				t.Errorf("first fixture event timestamp = %v, want %v", first, want)
			}
		})
	}
}

func TestUntimedEventKeepsFirstSeenTimestamp(t *testing.T) {
	const untimed = "Time Not Established^Time Not Established^Critical (3)^No Ranging Response received - T3 time-out"
	const timed = "09:00:00^Fri Aug 21 2026\n^Notice (6)^timed event"
	mock := fixtureClient().WithResponse(logAction, logResponse(timed, untimed))
	collector := NewCollector(mock, Config{}, slog.Default())
	now := time.Date(2026, time.August, 23, 5, 0, 0, 0, time.UTC)
	setCollectorClock(collector, func() time.Time { return now })
	collect := MakeCollectFunc(collector)

	firstSeen := now
	var firstID string
	for poll := 0; poll < 3; poll++ {
		metrics, err := collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		events := eventMetrics(metrics)
		if len(events) != 2 {
			t.Fatalf("poll %d event count = %d, want 2", poll, len(events))
		}
		if want := time.Date(2026, time.August, 21, 9, 0, 0, 0, time.UTC); !events[0].Timestamp.Equal(want) {
			t.Errorf("poll %d timed event timestamp = %v, want %v", poll, events[0].Timestamp, want)
		}
		if !events[1].Timestamp.Equal(firstSeen) {
			t.Errorf("poll %d untimed event timestamp = %v, want first-seen %v", poll, events[1].Timestamp, firstSeen)
		}
		if poll == 0 {
			firstID = events[1].Tags["doc_id"]
		} else if got := events[1].Tags["doc_id"]; got != firstID {
			t.Errorf("poll %d untimed doc_id = %q, want %q", poll, got, firstID)
		}
		now = now.Add(time.Minute)
	}

	// Once the entry leaves the modem log its first-seen time is forgotten.
	mock.WithResponse(logAction, logResponse(timed))
	if _, err := collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := firstSeenCount(collector); got != 0 {
		t.Fatalf("first-seen cache size after entry vanished = %d, want 0", got)
	}
}

func TestUntimedEventFirstSeenCacheIsBounded(t *testing.T) {
	previous := maxFirstSeenEvents
	maxFirstSeenEvents = 3
	t.Cleanup(func() { maxFirstSeenEvents = previous })

	entries := make([]string, 0, 10)
	for i := range 10 {
		entries = append(entries, fmt.Sprintf("Time Not Established^Time Not Established^Critical (3)^untimed %d", i))
	}
	mock := fixtureClient().WithResponse(logAction, logResponse(entries...))
	collector := NewCollector(mock, Config{}, slog.Default())
	metrics, err := MakeCollectFunc(collector)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(eventMetrics(metrics)); got != 10 {
		t.Fatalf("event count = %d, want 10 even past the cache bound", got)
	}
	if got := firstSeenCount(collector); got > 3 {
		t.Fatalf("first-seen cache size = %d, want at most 3", got)
	}
}

func TestAuthFailureEntersCooldown(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*hnaptest.MockClient)
	}{
		{name: "login rejected", setup: func(mock *hnaptest.MockClient) { mock.LoginErr = hnap.ErrAuthFailed }},
		{name: "re-login rejected during call", setup: func(mock *hnaptest.MockClient) {
			mock.WithError("GetMultipleHNAPs", fmt.Errorf("relogin: %w", hnap.ErrAuthFailed))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, time.August, 23, 5, 0, 0, 0, time.UTC)
			backoff := 6 * time.Minute
			mock := fixtureClient()
			test.setup(mock)
			collector := NewCollector(mock, Config{LockoutBackoff: backoff}, slog.Default())
			setCollectorClock(collector, func() time.Time { return now })
			collect := MakeCollectFunc(collector)

			if _, err := collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got, want := lockoutDeadline(collector), now.Add(backoff); !got.Equal(want) {
				t.Fatalf("cooldown deadline after auth failure = %v, want %v", got, want)
			}
			attempts, multiCalls := mock.EnsureLoginCalls, len(mock.LastMultiArgs)

			now = now.Add(backoff / 2)
			metrics, err := collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if mock.EnsureLoginCalls != attempts || len(mock.LastMultiArgs) != multiCalls {
				t.Fatalf("poll during auth-failure cooldown contacted the modem: login attempts %d -> %d", attempts, mock.EnsureLoginCalls)
			}
			poll := onlyMetric(t, metrics, "docsis_poll")
			if poll.Fields["success"] != 0 || poll.Fields["error"] != "lockout cooldown" {
				t.Fatalf("cooldown poll status = %v", poll.Fields)
			}
		})
	}
}

func TestPollStatusMetric(t *testing.T) {
	boom := errors.New("dial tcp 192.0.2.1:443: connect: connection refused")
	tests := []struct {
		name        string
		mock        func() *hnaptest.MockClient
		wantSuccess int
		wantError   string
		wantOthers  bool
	}{
		{name: "success", mock: fixtureClient, wantSuccess: 1, wantOthers: true},
		{name: "unreachable modem", mock: func() *hnaptest.MockClient {
			mock := fixtureClient()
			mock.LoginErr = boom
			return mock
		}, wantError: boom.Error()},
		{name: "multi failure", mock: func() *hnaptest.MockClient { return fixtureClient().WithError("GetMultipleHNAPs", boom) }, wantError: boom.Error()},
		{name: "lockout", mock: func() *hnaptest.MockClient {
			mock := fixtureClient()
			mock.LockedOut = true
			return mock
		}, wantError: hnap.ErrLockedOut.Error()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start := time.Date(2026, time.August, 23, 5, 0, 0, 0, time.UTC)
			now := start
			collector := NewCollector(test.mock(), Config{Host: "poller", ModemTag: "modem.lan"}, slog.Default())
			setCollectorClock(collector, func() time.Time {
				current := now
				now = now.Add(250 * time.Millisecond)
				return current
			})
			metrics, err := MakeCollectFunc(collector)(context.Background())
			if err != nil {
				t.Fatalf("CollectFunc error = %v, want nil so the status point is published", err)
			}
			poll := onlyMetric(t, metrics, "docsis_poll")
			if poll.Tags["modem"] != "modem.lan" || poll.Tags["host"] != "poller" {
				t.Errorf("poll tags = %v", poll.Tags)
			}
			if !poll.Timestamp.Equal(start) {
				t.Errorf("poll timestamp = %v, want poll start %v", poll.Timestamp, start)
			}
			if got := poll.Fields["success"]; got != test.wantSuccess {
				t.Errorf("success = %#v, want %d", got, test.wantSuccess)
			}
			if got := poll.Fields["error"]; got != test.wantError {
				t.Errorf("error = %#v, want %q", got, test.wantError)
			}
			if duration, ok := poll.Fields["duration_ms"].(float64); !ok || duration <= 0 {
				t.Errorf("duration_ms = %#v, want positive float64", poll.Fields["duration_ms"])
			}
			if got := len(metrics) > 1; got != test.wantOthers {
				t.Errorf("other metrics present = %t, want %t", got, test.wantOthers)
			}
		})
	}
}

func TestModemMetricKeepsParsedFields(t *testing.T) {
	for _, test := range []struct {
		name      string
		action    string
		response  json.RawMessage
		present   string
		dropped   string
		wantValue float64
	}{
		{
			name:      "invalid uptime",
			action:    connectionAction,
			response:  json.RawMessage(`{"MotoConnNetworkAccess":"Allowed","MotoConnSystemUpTime":"unknown","GetMotoStatusConnectionInfoResult":"OK"}`),
			present:   "ds_freq_mhz",
			dropped:   "uptime_seconds",
			wantValue: 237,
		},
		{
			name:      "frequency with MHz unit",
			action:    startupAction,
			response:  json.RawMessage(`{"MotoConnBootStatus":"OK","MotoConnConnectivityStatus":"OK","MotoConnSecurityStatus":"Enabled","MotoConnDSFreq":"237 MHz","GetMotoStatusStartupSequenceResult":"OK"}`),
			present:   "uptime_seconds",
			dropped:   "ds_freq_mhz",
			wantValue: (1083*time.Hour + 9*time.Minute + 15*time.Second).Seconds(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			metrics := collectFixtureMetrics(t, fixtureClient().WithResponse(test.action, test.response))
			modem := onlyMetric(t, metrics, "docsis_modem")
			if got := modem.Fields[test.present]; got != test.wantValue {
				t.Errorf("%s = %#v, want %v", test.present, got, test.wantValue)
			}
			if _, ok := modem.Fields[test.dropped]; ok {
				t.Errorf("%s present despite parse failure: %v", test.dropped, modem.Fields)
			}
			if modem.Fields["connectivity_ok"] != 1 || modem.Fields["connectivity_status"] != "OK" {
				t.Errorf("status fields missing: %v", modem.Fields)
			}
		})
	}
}

func TestChannelStatusFields(t *testing.T) {
	metrics := collectFixtureMetrics(t, fixtureClient().WithResponse(upstreamAction, json.RawMessage(
		`{"MotoConnUpstreamChannel":"1^Locked^SC-QAM^1^5120^17.6^42.3^|+|2^Not Locked^OFDMA^6^44000^36.8^37.2^","GetMotoStatusUpstreamChannelInfoResult":"OK"}`)))
	byChannel := map[string]*monitor.Metric{}
	for _, metric := range metrics {
		if metric.Measurement == "docsis_upstream" {
			byChannel[metric.Tags["channel_id"]] = metric
		}
	}
	if got := byChannel["1"].Fields; got["locked"] != 1 || got["lock_status"] != "Locked" || got["channel_type"] != "SC-QAM" {
		t.Errorf("locked upstream fields = %v", got)
	}
	if got := byChannel["6"].Fields; got["locked"] != 0 || got["lock_status"] != "Not Locked" || got["channel_type"] != "OFDMA" {
		t.Errorf("unlocked upstream fields = %v", got)
	}
	for _, metric := range metrics {
		if metric.Measurement == "docsis_downstream" {
			if metric.Fields["locked"] != 1 || metric.Fields["modulation"] == "" {
				t.Errorf("downstream status fields = %v", metric.Fields)
			}
		}
	}
}

func TestIndexTemplateMapsEveryEventField(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "index-template.json"))
	if err != nil {
		t.Fatal(err)
	}
	var template struct {
		IndexPatterns []string `json:"index_patterns"`
		Priority      int      `json:"priority"`
		Template      struct {
			Mappings struct {
				Dynamic    any                        `json:"dynamic"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"mappings"`
		} `json:"template"`
	}
	if err := json.Unmarshal(contents, &template); err != nil {
		t.Fatal(err)
	}
	if len(template.IndexPatterns) != 1 || template.IndexPatterns[0] != "mb8611-events*" {
		t.Errorf("index_patterns = %v", template.IndexPatterns)
	}
	if template.Priority <= 100 {
		t.Errorf("priority = %d, want above 100", template.Priority)
	}
	if template.Template.Mappings.Dynamic != "false" && template.Template.Mappings.Dynamic != false {
		t.Errorf("dynamic = %#v, want false", template.Template.Mappings.Dynamic)
	}

	event := eventMetrics(collectFixtureMetrics(t, fixtureClient()))[0]
	documentFields := []string{"@timestamp", "measurement"}
	for tag := range event.Tags {
		documentFields = append(documentFields, tag)
	}
	for field := range event.Fields {
		documentFields = append(documentFields, field)
	}
	wantTypes := map[string]string{"@timestamp": "date", "message": "text"}
	for _, field := range documentFields {
		raw, ok := template.Template.Mappings.Properties[field]
		if !ok {
			t.Errorf("event document field %q is not mapped", field)
			continue
		}
		var mapping struct {
			Type   string                     `json:"type"`
			Fields map[string]json.RawMessage `json:"fields"`
		}
		if err := json.Unmarshal(raw, &mapping); err != nil {
			t.Fatal(err)
		}
		want := wantTypes[field]
		if want == "" {
			want = "keyword"
		}
		if mapping.Type != want {
			t.Errorf("field %q type = %q, want %q", field, mapping.Type, want)
		}
		if field == "message" && mapping.Fields["keyword"] == nil {
			t.Errorf("message has no keyword subfield")
		}
	}
}

func firstSeenCount(collector *Collector) int {
	collector.pollMu.Lock()
	defer collector.pollMu.Unlock()
	return len(collector.firstSeen)
}
