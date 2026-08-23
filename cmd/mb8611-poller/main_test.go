package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		"  insecure_tls: true\n" +
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
	if got := run([]string{"run-once", "--echo", "--config", configPath}, &output, io.Discard); got != 0 {
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

func TestCommandDispatchAndPersistentFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    commandOptions
		runOnce bool
	}{
		{
			name:    "flags before run",
			args:    []string{"--config", "before.yaml", "run", "--echo"},
			want:    commandOptions{configPath: "before.yaml", echo: true},
			runOnce: false,
		},
		{
			name:    "flags after run once",
			args:    []string{"run-once", "--echo", "--config", "after.yaml"},
			want:    commandOptions{configPath: "after.yaml", echo: true},
			runOnce: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			got := executeCommand(tt.args, io.Discard, io.Discard, func(options commandOptions, runOnce bool, _, _ io.Writer) int {
				called = true
				if options != tt.want {
					t.Errorf("options = %#v, want %#v", options, tt.want)
				}
				if runOnce != tt.runOnce {
					t.Errorf("runOnce = %t, want %t", runOnce, tt.runOnce)
				}
				return 0
			})
			if got != 0 {
				t.Fatalf("executeCommand() = %d, want 0", got)
			}
			if !called {
				t.Fatal("monitor runner was not called")
			}
		})
	}
}

func TestCommandHelpAndUsageErrors(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no command", wantCode: 2, wantStderr: "a command is required"},
		{name: "flags without command", args: []string{"--config", "test.yaml", "--echo"}, wantCode: 2, wantStderr: "a command is required"},
		{name: "root help", args: []string{"--help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "command help", args: []string{"run-once", "--help"}, wantCode: 0, wantStdout: "Usage:"},
		{name: "unknown command", args: []string{"unknown"}, wantCode: 2, wantStderr: "unknown command"},
		{name: "invalid flag", args: []string{"run", "--unknown"}, wantCode: 2, wantStderr: "unknown flag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			called := false
			got := executeCommand(tt.args, &stdout, &stderr, func(commandOptions, bool, io.Writer, io.Writer) int {
				called = true
				return 0
			})
			if got != tt.wantCode {
				t.Fatalf("executeCommand() = %d, want %d", got, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
			if tt.wantStdout == "" && stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if tt.wantStderr == "" && stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
			if called {
				t.Fatal("monitor runner was called")
			}
		})
	}
}

func TestRuntimeDiagnosticsUseStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"run-once", "--config", filepath.Join(t.TempDir(), "missing.yaml")}, &stdout, &stderr); got != 1 {
		t.Fatalf("run() = %d, want 1", got)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "load config:") {
		t.Errorf("stderr = %q, want config load diagnostic", stderr.String())
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
