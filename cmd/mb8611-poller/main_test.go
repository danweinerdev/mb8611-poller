package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	monitor "github.com/danweinerdev/go-monitor"
	"github.com/danweinerdev/mb8611-poller/internal/config"
	"github.com/danweinerdev/mb8611-poller/internal/hnap/hnaptest"
)

func TestRunEchoOnce(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{
		Password:  "secret",
		Responses: fixtureResponses(t),
	})
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := "modem:\n" +
		"  host: " + serverURL.Host + "\n" +
		"  username: admin\n" +
		"  password: secret\n" +
		"interval: 1m\n" +
		"hostname: poller-host\n" +
		"log_level: info\n" +
		"influxdb:\n" +
		"  enabled: true\n" +
		"elasticsearch:\n" +
		"  enabled: true\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if got := run([]string{"--config", configPath, "--echo", "--run-once"}, &output); got != 0 {
		t.Fatalf("run() = %d, output: %s", got, output.String())
	}
	for _, measurement := range []string{"docsis_downstream,", "docsis_upstream,", "docsis_modem,", "docsis_event,"} {
		if !strings.Contains(output.String(), measurement) {
			t.Errorf("echo output missing %q:\n%s", measurement, output.String())
		}
	}
	if !strings.Contains(output.String(), "host=poller-host") {
		t.Errorf("echo output missing configured hostname tag:\n%s", output.String())
	}
}

func TestBuildBackendsOmitsDisabledAndValidatesRouting(t *testing.T) {
	logger, _ := monitor.NewLogger("info")
	cfg := &config.Config{
		InfluxDB: config.InfluxDBConfig{Enabled: true},
		Routing: config.RoutingConfig{
			InfluxDBInclude:      []string{"docsis_*"},
			ElasticsearchInclude: []string{"docsis_event"},
		},
	}
	backends, err := buildBackends(cfg, false, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}
	cfg.Routing.InfluxDBInclude = []string{"*invalid*"}
	if _, err := buildBackends(cfg, false, nil, logger); err == nil {
		t.Fatal("invalid routing pattern was accepted")
	}
}

func TestReloadFuncUpdatesRuntimeConfig(t *testing.T) {
	_, levelVar := monitor.NewLogger("info")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	want := &config.Config{
		Interval: 2 * time.Minute,
		LogLevel: "debug",
	}
	loads := 0
	reloadFn := newReloadFunc(configPath, func(path string) (*config.Config, error) {
		loads++
		if path != configPath {
			t.Errorf("reload config path = %q, want %q", path, configPath)
		}
		return want, nil
	}, levelVar)

	got, err := reloadFn("ignored-by-go-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("config loads = %d, want 1", loads)
	}
	if got.Global.PollInterval.Duration != want.Interval {
		t.Errorf("poll interval = %s, want %s", got.Global.PollInterval.Duration, want.Interval)
	}
	if got.Global.LogLevel != want.LogLevel {
		t.Errorf("log level = %q, want %q", got.Global.LogLevel, want.LogLevel)
	}
	if got := levelVar.Level(); got != slog.LevelDebug {
		t.Errorf("running log level = %s, want %s", got, slog.LevelDebug)
	}
}

func TestReloadDoesNotChangeRunningElasticsearchBackend(t *testing.T) {
	var originalWrites, reloadedRequests int
	originalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "_bulk") {
			originalWrites++
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		_, _ = w.Write([]byte(`{"errors":false,"items":[{"index":{"status":201}}]}`))
	}))
	defer originalServer.Close()
	reloadedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reloadedRequests++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		_, _ = w.Write([]byte(`{"errors":false,"items":[{"index":{"status":201}}]}`))
	}))
	defer reloadedServer.Close()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeReloadConfig(t, configPath, originalServer.URL, "1m", "info")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	logger, levelVar := monitor.NewLogger(cfg.LogLevel)
	backends, err := buildBackends(cfg, false, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	if len(backends) != 1 {
		t.Fatalf("backend count = %d, want 1", len(backends))
	}
	elasticsearchBackend := backends[0]
	filter, ok := elasticsearchBackend.(*monitor.MeasurementFilter)
	if !ok {
		t.Fatalf("Elasticsearch backend type = %T, want *monitor.MeasurementFilter", elasticsearchBackend)
	}
	runningBackend := filter.Inner

	writeReloadConfig(t, configPath, reloadedServer.URL, "2m", "warn")
	reloadFn := newReloadFunc(configPath, config.LoadConfig, levelVar)
	reloadedMonitorConfig, err := reloadFn("ignored-by-go-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if got := reloadedMonitorConfig.Global.PollInterval.Duration; got != 2*time.Minute {
		t.Errorf("reloaded poll interval = %s, want 2m", got)
	}
	if got := levelVar.Level(); got != slog.LevelWarn {
		t.Errorf("running log level = %s, want %s", got, slog.LevelWarn)
	}
	if filter.Inner != runningBackend {
		t.Error("reload replaced the running Elasticsearch backend")
	}

	if err := filter.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := filter.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := filter.Write(context.Background(), []*monitor.Metric{
		monitor.NewMetric("docsis_event").WithField("message", "test"),
	}); err != nil {
		t.Fatal(err)
	}
	if originalWrites != 1 {
		t.Errorf("writes to original Elasticsearch URL = %d, want 1", originalWrites)
	}
	if reloadedRequests != 0 {
		t.Errorf("requests to reloaded Elasticsearch URL = %d, want 0", reloadedRequests)
	}
}

func writeReloadConfig(t *testing.T, path, elasticsearchURL, interval, logLevel string) {
	t.Helper()
	contents := fmt.Sprintf(`modem:
  host: modem.example
  password: secret
interval: %s
log_level: %s
elasticsearch:
  enabled: true
  url: %q
  index: events
  api_key: test-key
`, interval, logLevel, elasticsearchURL)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureResponses(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	actions := []string{
		"GetMotoStatusDownstreamChannelInfo",
		"GetMotoStatusUpstreamChannelInfo",
		"GetMotoStatusConnectionInfo",
		"GetMotoStatusSoftware",
		"GetMotoStatusStartupSequence",
		"GetMotoStatusLog",
	}
	responses := make(map[string]json.RawMessage, len(actions))
	for _, action := range actions {
		contents, err := os.ReadFile(filepath.Join("..", "..", "internal", "hnap", "hnaptest", "testdata", action+".json"))
		if err != nil {
			t.Fatal(err)
		}
		responses[action] = contents
	}
	return responses
}
