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
	"MB8611_MODEM_INSECURE_TLS",
	"MB8611_MODEM_TLS_FINGERPRINT",
	"MB8611_MODEM_EVENT_TIMEZONE",
	"MB8611_INFLUXDB_ENABLED",
	"MB8611_ES_ENABLED",
}

const testFingerprint = "ab:cd:ef:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:ab:cd:ef:01:23:45:67:89:ab:cd:ef:01:23:45:67:89"
const testFingerprintNormalized = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

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
  username: admin
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
	if cfg.Modem.InsecureTLS || cfg.Modem.LockoutBackoff != defaultLockoutBackoff {
		t.Fatalf("unexpected modem defaults: %#v", cfg.Modem)
	}
	if cfg.Modem.Timeout != 10*time.Second {
		t.Fatalf("default modem timeout = %s, want 10s", cfg.Modem.Timeout)
	}
	if cfg.Modem.EventTimezone != "UTC" || cfg.Modem.EventLocation != time.UTC {
		t.Fatalf("default event timezone = %q (%v), want UTC", cfg.Modem.EventTimezone, cfg.Modem.EventLocation)
	}
	if cfg.Modem.TLSFingerprintSHA256 != "" {
		t.Fatalf("default TLS fingerprint = %q, want empty", cfg.Modem.TLSFingerprintSHA256)
	}
	if got := strings.Join(cfg.Routing.InfluxDBInclude, ","); got != "docsis_downstream,docsis_upstream,docsis_modem,docsis_poll" {
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
		{name: "empty modem username", yaml: strings.Replace(validYAML, "username: modem-user", "username: ' '", 1), field: "modem.username"},
		{name: "modem host with path", yaml: strings.Replace(validYAML, "host: modem.example", "host: https://modem.example/HNAP1/", 1), field: "modem.host"},
		{name: "invalid event timezone", yaml: strings.Replace(validYAML, "lockout_backoff: 7m", "lockout_backoff: 7m\n  event_timezone: Mars/Olympus_Mons", 1), field: "modem.event_timezone"},
		{name: "short TLS fingerprint", yaml: strings.Replace(validYAML, "lockout_backoff: 7m", "lockout_backoff: 7m\n  tls_fingerprint_sha256: abcd", 1), field: "modem.tls_fingerprint_sha256"},
		{name: "non-hex TLS fingerprint", yaml: strings.Replace(validYAML, "lockout_backoff: 7m", "lockout_backoff: 7m\n  tls_fingerprint_sha256: "+strings.Repeat("zz", 32), 1), field: "modem.tls_fingerprint_sha256"},
		{name: "non-positive modem timeout", yaml: strings.Replace(validYAML, "lockout_backoff: 7m", "lockout_backoff: 7m\n  timeout: 0s", 1), field: "modem.timeout"},
		{name: "empty modem password", yaml: strings.Replace(validYAML, "password: modem-password", "password: ''", 1), field: "modem.password"},
		{name: "non-positive lockout backoff", yaml: strings.Replace(validYAML, "lockout_backoff: 7m", "lockout_backoff: 0s", 1), field: "modem.lockout_backoff"},
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
		{name: "MB8611_MODEM_INSECURE_TLS", value: "true", check: func(cfg *Config) bool { return cfg.Modem.InsecureTLS }},
		{name: "MB8611_MODEM_TLS_FINGERPRINT", value: testFingerprint, check: func(cfg *Config) bool { return cfg.Modem.TLSFingerprintSHA256 == testFingerprintNormalized }},
		{name: "MB8611_MODEM_EVENT_TIMEZONE", value: "America/Chicago", check: func(cfg *Config) bool {
			return cfg.Modem.EventTimezone == "America/Chicago" && cfg.Modem.EventLocation.String() == "America/Chicago"
		}},
		{name: "MB8611_INFLUXDB_ENABLED", value: "false", check: func(cfg *Config) bool { return !cfg.InfluxDB.Enabled }},
		{name: "MB8611_ES_ENABLED", value: "0", check: func(cfg *Config) bool { return !cfg.Elasticsearch.Enabled }},
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

func TestLoadConfigRejectsMalformedEnvOverrides(t *testing.T) {
	for name, value := range map[string]string{
		"MB8611_INTERVAL":           "not-a-duration",
		"MB8611_MODEM_INSECURE_TLS": "maybe",
		"MB8611_INFLUXDB_ENABLED":   "yes please",
		"MB8611_ES_ENABLED":         "on",
	} {
		t.Run(name, func(t *testing.T) {
			clearEnvOverrides(t)
			t.Setenv(name, value)
			_, err := LoadConfig(writeConfig(t, validYAML))
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("LoadConfig() error = %v, want an error naming %s", err, name)
			}
		})
	}
}

func TestLoadConfigEnablesBackendsFromEnv(t *testing.T) {
	clearEnvOverrides(t)
	t.Setenv("MB8611_INFLUXDB_ENABLED", "true")
	t.Setenv("MB8611_ES_ENABLED", "1")
	disabled := strings.Replace(validYAML, "enabled: true", "enabled: false", -1)
	cfg := loadConfigWithEnv(t, disabled)
	if !cfg.InfluxDB.Enabled || !cfg.Elasticsearch.Enabled {
		t.Fatalf("backends enabled = influx %t, es %t; want both true", cfg.InfluxDB.Enabled, cfg.Elasticsearch.Enabled)
	}
}

func TestLoadConfigNormalizesModemSettings(t *testing.T) {
	cfg := loadConfig(t, strings.Replace(strings.Replace(validYAML,
		"host: modem.example", "host: https://modem.example/", 1),
		"lockout_backoff: 7m", "lockout_backoff: 7m\n  timeout: 3s\n  event_timezone: America/New_York\n  tls_fingerprint_sha256: \""+testFingerprint+"\"", 1))
	if cfg.Modem.Host != "modem.example" {
		t.Errorf("modem host = %q, want scheme and slash stripped", cfg.Modem.Host)
	}
	if cfg.Modem.Timeout != 3*time.Second {
		t.Errorf("modem timeout = %s, want 3s", cfg.Modem.Timeout)
	}
	if cfg.Modem.EventLocation == nil || cfg.Modem.EventLocation.String() != "America/New_York" {
		t.Errorf("event location = %v, want America/New_York", cfg.Modem.EventLocation)
	}
	if cfg.Modem.TLSFingerprintSHA256 != testFingerprintNormalized {
		t.Errorf("fingerprint = %q, want %q", cfg.Modem.TLSFingerprintSHA256, testFingerprintNormalized)
	}
}

func TestLoadModemConfigNeedsNoCredentials(t *testing.T) {
	clearEnvOverrides(t)
	cfg, err := LoadModemConfig(writeConfig(t, "modem:\n  host: https://modem.example\n"))
	if err != nil {
		t.Fatalf("LoadModemConfig() error = %v", err)
	}
	if cfg.Modem.Host != "modem.example" || cfg.Modem.Timeout != 10*time.Second {
		t.Fatalf("modem config = %#v", cfg.Modem)
	}
	if _, err := LoadModemConfig(writeConfig(t, "interval: 1m\n")); err == nil || !strings.Contains(err.Error(), "modem.host:") {
		t.Fatalf("LoadModemConfig() without host error = %v, want modem.host error", err)
	}
}

func TestLoadConfigDoesNotInterpolateYAMLVariables(t *testing.T) {
	cfg := loadConfig(t, strings.Replace(strings.Replace(validYAML, "username: modem-user", "username: ${MB8611_MODEM_USERNAME}", 1), "password: modem-password", "password: ${MB8611_MODEM_PASSWORD}", 1))
	if cfg.Modem.Username != "${MB8611_MODEM_USERNAME}" || cfg.Modem.Password != "${MB8611_MODEM_PASSWORD}" {
		t.Fatalf("YAML values were interpolated: %#v", cfg.Modem)
	}

	// A literal ${...} host is kept verbatim and then rejected as a host.
	clearEnvOverrides(t)
	_, err := LoadConfig(writeConfig(t, strings.Replace(validYAML, "host: modem.example", "host: ${MB8611_MODEM_HOST}", 1)))
	if err == nil || !strings.Contains(err.Error(), "${MB8611_MODEM_HOST}") {
		t.Fatalf("LoadConfig() error = %v, want rejection quoting the literal host", err)
	}
}

func TestConfigConverters(t *testing.T) {
	cfg := loadConfig(t, validYAML)
	monitorConfig := cfg.MonitorConfig()
	if monitorConfig.Global.PollInterval.Duration != 45*time.Second || monitorConfig.Global.LogLevel != "warn" {
		t.Fatalf("unexpected monitor config: %#v", monitorConfig.Global)
	}
	if got, want := monitorConfig.Global.BatchSize, 1000; got != want {
		t.Fatalf("MonitorConfig batch size = %d, want %d (above one poll's output)", got, want)
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
