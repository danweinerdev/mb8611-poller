package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	monitor "github.com/danweinerdev/go-monitor"
)

const validYAML = `
modem:
  host: modem.example
  username: modem-user
  password: modem-password
  insecure_tls: false
  lockout_backoff: 7m
interval: 45s
hostname: configured-host
log_level: warn
influxdb:
  enabled: true
  url: http://influx.example:8086
  token: influx-token
  org: home
  bucket: modem
elasticsearch:
  enabled: true
  url: https://es.example:9200
  index: mb8611-events
  api_key: es-key
  username: es-user
  password: es-password
  insecure_tls: true
  id_from_tag: event-id
routing:
  influxdb_include: [custom-influx]
  elasticsearch_include: [custom-es]
`

var envOverrides = []string{
	"MB8611_MODEM_HOST",
	"MB8611_MODEM_USERNAME",
	"MB8611_MODEM_PASSWORD",
	"MB8611_INFLUXDB_URL",
	"MB8611_INFLUXDB_TOKEN",
	"MB8611_INFLUXDB_ORG",
	"MB8611_INFLUXDB_BUCKET",
	"MB8611_ES_URL",
	"MB8611_ES_INDEX",
	"MB8611_ES_API_KEY",
	"MB8611_ES_USERNAME",
	"MB8611_ES_PASSWORD",
	"MB8611_INTERVAL",
	"MB8611_HOSTNAME",
	"MB8611_LOG_LEVEL",
}

func TestLoadConfig(t *testing.T) {
	cfg := loadConfig(t, validYAML)

	if cfg.Modem.Host != "modem.example" || cfg.Modem.Username != "modem-user" || cfg.Modem.Password != "modem-password" {
		t.Fatalf("unexpected modem config: %#v", cfg.Modem)
	}
	if cfg.Modem.InsecureTLS || cfg.Modem.LockoutBackoff != 7*time.Minute {
		t.Fatalf("unexpected modem defaults from YAML: %#v", cfg.Modem)
	}
	if cfg.Interval != 45*time.Second || cfg.Hostname != "configured-host" || cfg.LogLevel != "warn" {
		t.Fatalf("unexpected top-level config: %#v", cfg)
	}
	if !cfg.InfluxDB.Enabled || cfg.InfluxDB.Bucket != "modem" || !cfg.Elasticsearch.Enabled || cfg.Elasticsearch.IDFromTag != "event-id" {
		t.Fatalf("unexpected backend config: influx=%#v elasticsearch=%#v", cfg.InfluxDB, cfg.Elasticsearch)
	}
	if got := strings.Join(cfg.Routing.InfluxDBInclude, ","); got != "custom-influx" {
		t.Fatalf("InfluxDB include = %q", got)
	}
	if got := strings.Join(cfg.Routing.ElasticsearchInclude, ","); got != "custom-es" {
		t.Fatalf("Elasticsearch include = %q", got)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg := loadConfig(t, `
modem:
  host: modem.example
  password: password
interval: 1m
log_level: info
`)

	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("Hostname() error = %v", err)
	}
	if cfg.Hostname != hostname {
		t.Fatalf("hostname = %q, want %q", cfg.Hostname, hostname)
	}
	if !cfg.Modem.InsecureTLS || cfg.Modem.LockoutBackoff != defaultLockoutBackoff {
		t.Fatalf("unexpected modem defaults: %#v", cfg.Modem)
	}
	if got := strings.Join(cfg.Routing.InfluxDBInclude, ","); got != "docsis_downstream,docsis_upstream,docsis_modem" {
		t.Fatalf("default InfluxDB include = %q", got)
	}
	if got := strings.Join(cfg.Routing.ElasticsearchInclude, ","); got != "docsis_event" {
		t.Fatalf("default Elasticsearch include = %q", got)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		field string
	}{
		{name: "empty modem host", yaml: strings.Replace(validYAML, "host: modem.example", "host: ''", 1), field: "modem.host"},
		{name: "empty modem password", yaml: strings.Replace(validYAML, "password: modem-password", "password: ''", 1), field: "modem.password"},
		{name: "missing influx URL", yaml: strings.Replace(validYAML, "url: http://influx.example:8086", "url: ''", 1), field: "influxdb.url"},
		{name: "missing influx token", yaml: strings.Replace(validYAML, "token: influx-token", "token: ''", 1), field: "influxdb.token"},
		{name: "missing influx org", yaml: strings.Replace(validYAML, "org: home", "org: ''", 1), field: "influxdb.org"},
		{name: "missing influx bucket", yaml: strings.Replace(validYAML, "bucket: modem", "bucket: ''", 1), field: "influxdb.bucket"},
		{name: "missing Elasticsearch URL", yaml: strings.Replace(validYAML, "url: https://es.example:9200", "url: ''", 1), field: "elasticsearch.url"},
		{name: "missing Elasticsearch index", yaml: strings.Replace(validYAML, "index: mb8611-events", "index: ''", 1), field: "elasticsearch.index"},
		{name: "missing Elasticsearch authentication", yaml: strings.Replace(strings.Replace(strings.Replace(validYAML, "api_key: es-key", "api_key: ''", 1), "username: es-user", "username: ''", 1), "password: es-password", "password: ''", 1), field: "elasticsearch.authentication"},
		{name: "non-positive interval", yaml: strings.Replace(validYAML, "interval: 45s", "interval: 0s", 1), field: "interval"},
		{name: "unknown log level", yaml: strings.Replace(validYAML, "log_level: warn", "log_level: trace", 1), field: "log_level"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfig(t, test.yaml)
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("LoadConfig() error = nil")
			}
			var validationErrors monitor.ValidationErrors
			if !errors.As(err, &validationErrors) {
				t.Fatalf("LoadConfig() error = %T %v, want monitor.ValidationErrors", err, err)
			}
			if !strings.Contains(err.Error(), test.field+":") {
				t.Fatalf("LoadConfig() error = %q, want field %q", err, test.field)
			}
		})
	}
}

func TestLoadConfigForEchoSkipsOnlyBackendValidation(t *testing.T) {
	config := strings.Replace(strings.Replace(strings.Replace(strings.Replace(strings.Replace(strings.Replace(strings.Replace(strings.Replace(validYAML,
		"url: http://influx.example:8086", "url: ''", 1),
		"token: influx-token", "token: ''", 1),
		"org: home", "org: ''", 1),
		"bucket: modem", "bucket: ''", 1),
		"url: https://es.example:9200", "url: ''", 1),
		"index: mb8611-events", "index: ''", 1),
		"api_key: es-key", "api_key: ''", 1),
		"username: es-user", "username: ''", 1)
	if _, err := LoadConfigForEcho(writeConfig(t, config)); err != nil {
		t.Fatalf("LoadConfigForEcho() error = %v", err)
	}

	config = strings.Replace(config, "interval: 45s", "interval: 0s", 1)
	_, err := LoadConfigForEcho(writeConfig(t, config))
	if err == nil || !strings.Contains(err.Error(), "interval:") {
		t.Fatalf("LoadConfigForEcho() error = %v, want interval validation error", err)
	}
}

func TestLoadConfigEnvOverrides(t *testing.T) {
	tests := []struct {
		name  string
		value string
		check func(*Config) bool
	}{
		{name: "MB8611_MODEM_HOST", value: "override-modem", check: func(cfg *Config) bool { return cfg.Modem.Host == "override-modem" }},
		{name: "MB8611_MODEM_USERNAME", value: "override-modem-user", check: func(cfg *Config) bool { return cfg.Modem.Username == "override-modem-user" }},
		{name: "MB8611_MODEM_PASSWORD", value: "override-modem-password", check: func(cfg *Config) bool { return cfg.Modem.Password == "override-modem-password" }},
		{name: "MB8611_INFLUXDB_URL", value: "http://override-influx:8086", check: func(cfg *Config) bool { return cfg.InfluxDB.URL == "http://override-influx:8086" }},
		{name: "MB8611_INFLUXDB_TOKEN", value: "override-influx-token", check: func(cfg *Config) bool { return cfg.InfluxDB.Token == "override-influx-token" }},
		{name: "MB8611_INFLUXDB_ORG", value: "override-influx-org", check: func(cfg *Config) bool { return cfg.InfluxDB.Org == "override-influx-org" }},
		{name: "MB8611_INFLUXDB_BUCKET", value: "override-influx-bucket", check: func(cfg *Config) bool { return cfg.InfluxDB.Bucket == "override-influx-bucket" }},
		{name: "MB8611_ES_URL", value: "https://override-es:9200", check: func(cfg *Config) bool { return cfg.Elasticsearch.URL == "https://override-es:9200" }},
		{name: "MB8611_ES_INDEX", value: "override-es-index", check: func(cfg *Config) bool { return cfg.Elasticsearch.Index == "override-es-index" }},
		{name: "MB8611_ES_API_KEY", value: "override-es-key", check: func(cfg *Config) bool { return cfg.Elasticsearch.APIKey == "override-es-key" }},
		{name: "MB8611_ES_USERNAME", value: "override-es-user", check: func(cfg *Config) bool { return cfg.Elasticsearch.Username == "override-es-user" }},
		{name: "MB8611_ES_PASSWORD", value: "override-es-password", check: func(cfg *Config) bool { return cfg.Elasticsearch.Password == "override-es-password" }},
		{name: "MB8611_INTERVAL", value: "2m", check: func(cfg *Config) bool { return cfg.Interval == 2*time.Minute }},
		{name: "MB8611_HOSTNAME", value: "override-hostname", check: func(cfg *Config) bool { return cfg.Hostname == "override-hostname" }},
		{name: "MB8611_LOG_LEVEL", value: "error", check: func(cfg *Config) bool { return cfg.LogLevel == "error" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearEnvOverrides(t)
			t.Setenv(test.name, test.value)
			cfg := loadConfigWithEnv(t, validYAML)
			if !test.check(cfg) {
				t.Fatalf("%s did not override configuration: %#v", test.name, cfg)
			}
		})
	}
}

func TestLoadConfigIgnoresInvalidIntervalOverride(t *testing.T) {
	clearEnvOverrides(t)
	t.Setenv("MB8611_INTERVAL", "not-a-duration")
	cfg := loadConfigWithEnv(t, validYAML)
	if cfg.Interval != 45*time.Second {
		t.Fatalf("interval = %s, want YAML value 45s", cfg.Interval)
	}
}

func TestLoadConfigDoesNotInterpolateYAMLVariables(t *testing.T) {
	cfg := loadConfig(t, strings.Replace(strings.Replace(validYAML, "host: modem.example", "host: ${MB8611_MODEM_HOST}", 1), "password: modem-password", "password: ${MB8611_MODEM_PASSWORD}", 1))
	if cfg.Modem.Host != "${MB8611_MODEM_HOST}" || cfg.Modem.Password != "${MB8611_MODEM_PASSWORD}" {
		t.Fatalf("YAML values were interpolated: %#v", cfg.Modem)
	}
}

func TestConfigConverters(t *testing.T) {
	cfg := loadConfig(t, validYAML)
	monitorConfig := cfg.MonitorConfig()
	if monitorConfig.Global.PollInterval.Duration != 45*time.Second || monitorConfig.Global.LogLevel != "warn" {
		t.Fatalf("unexpected monitor config: %#v", monitorConfig.Global)
	}
	if monitorConfig.InfluxDB != cfg.InfluxBackendConfig() {
		t.Fatalf("MonitorConfig InfluxDB = %#v, want %#v", monitorConfig.InfluxDB, cfg.InfluxBackendConfig())
	}
	esConfig := cfg.ESBackendConfig()
	if !esConfig.Enabled || esConfig.URL != cfg.Elasticsearch.URL || esConfig.Index != cfg.Elasticsearch.Index || esConfig.Username != cfg.Elasticsearch.Username || esConfig.Password != cfg.Elasticsearch.Password || esConfig.APIKey != cfg.Elasticsearch.APIKey || !esConfig.InsecureTLS || esConfig.IDFromTag != "event-id" {
		t.Fatalf("unexpected Elasticsearch config: %#v", esConfig)
	}
}

func loadConfig(t *testing.T, data string) *Config {
	t.Helper()
	clearEnvOverrides(t)
	return loadConfigWithEnv(t, data)
}

func loadConfigWithEnv(t *testing.T, data string) *Config {
	t.Helper()
	cfg, err := LoadConfig(writeConfig(t, data))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	return cfg
}

func writeConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func clearEnvOverrides(t *testing.T) {
	t.Helper()
	for _, name := range envOverrides {
		t.Setenv(name, "")
	}
}
