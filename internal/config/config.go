// Package config loads and validates the mb8611-poller configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	// Embed the IANA database so modem.event_timezone resolves in minimal
	// container images that ship no /usr/share/zoneinfo.
	_ "time/tzdata"

	monitor "github.com/danweinerdev/go-monitor"
	"github.com/danweinerdev/go-monitor/elasticsearch"
	"github.com/danweinerdev/mb8611-poller/internal/hnap"
	"gopkg.in/yaml.v3"
)

const (
	defaultLockoutBackoff = 6 * time.Minute
	defaultModemTimeout   = 10 * time.Second
	defaultEventTimezone  = "UTC"

	// batchSize keeps a whole poll (about 80 metrics on an MB8611, a few
	// hundred with a full event log) in one go-monitor batch, so writes happen
	// on the flush interval instead of in untracked mid-poll async flushes.
	batchSize = 1000
)

var (
	defaultInfluxDBInclude      = []string{"docsis_downstream", "docsis_upstream", "docsis_modem", "docsis_poll"}
	defaultElasticsearchInclude = []string{"docsis_event"}
)

// Config is the YAML configuration for mb8611-poller.
type Config struct {
	Modem         ModemConfig         `yaml:"modem"`
	Interval      time.Duration       `yaml:"interval"`
	Hostname      string              `yaml:"hostname"`
	LogLevel      string              `yaml:"log_level"`
	InfluxDB      InfluxDBConfig      `yaml:"influxdb"`
	Elasticsearch ElasticsearchConfig `yaml:"elasticsearch"`
	Routing       RoutingConfig       `yaml:"routing"`
}

// ModemConfig contains connection and lockout settings for the MB8611.
type ModemConfig struct {
	Host           string        `yaml:"host"`
	Username       string        `yaml:"username"`
	Password       string        `yaml:"password"`
	InsecureTLS    bool          `yaml:"insecure_tls"`
	LockoutBackoff time.Duration `yaml:"lockout_backoff"`
	// Timeout bounds each HNAP HTTP request. Defaults to 10s.
	Timeout time.Duration `yaml:"timeout"`
	// EventTimezone is the IANA zone the modem's event-log times are in.
	// Defaults to UTC.
	EventTimezone string `yaml:"event_timezone"`
	// TLSFingerprintSHA256 pins the modem's leaf certificate. It is stored
	// normalized: 64 lowercase hex digits, or empty when not pinned.
	TLSFingerprintSHA256 string `yaml:"tls_fingerprint_sha256"`
	// EventLocation is EventTimezone resolved at load time.
	EventLocation *time.Location `yaml:"-"`
}

// InfluxDBConfig contains the InfluxDB connection settings.
type InfluxDBConfig struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
	Token   string `yaml:"token"`
	Org     string `yaml:"org"`
	Bucket  string `yaml:"bucket"`
}

// ElasticsearchConfig contains the Elasticsearch connection settings.
type ElasticsearchConfig struct {
	Enabled     bool   `yaml:"enabled"`
	URL         string `yaml:"url"`
	Index       string `yaml:"index"`
	APIKey      string `yaml:"api_key"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	InsecureTLS bool   `yaml:"insecure_tls"`
	IDFromTag   string `yaml:"id_from_tag"`
}

// RoutingConfig contains the measurement allowlists for each backend.
type RoutingConfig struct {
	InfluxDBInclude      []string `yaml:"influxdb_include"`
	ElasticsearchInclude []string `yaml:"elasticsearch_include"`
}

// LoadConfig reads, applies environment overrides to, defaults, and validates
// the configuration at path.
func LoadConfig(path string) (*Config, error) {
	return loadConfigWithBackendValidation(path, true)
}

// LoadConfigForEcho reads configuration for echo mode. Echo replaces the
// production backends, so their enabled connection settings are not required.
// Modem and runtime settings are validated normally.
func LoadConfigForEcho(path string) (*Config, error) {
	return loadConfigWithBackendValidation(path, false)
}

func loadConfigWithBackendValidation(path string, validateBackends bool) (*Config, error) {
	cfg, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	if cfg.Hostname == "" {
		cfg.Hostname, err = os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("get hostname: %w", err)
		}
	}
	if len(cfg.Routing.InfluxDBInclude) == 0 {
		cfg.Routing.InfluxDBInclude = append([]string(nil), defaultInfluxDBInclude...)
	}
	if len(cfg.Routing.ElasticsearchInclude) == 0 {
		cfg.Routing.ElasticsearchInclude = append([]string(nil), defaultElasticsearchInclude...)
	}

	if err := validate(cfg, validateBackends); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadModemConfig reads configuration for commands that only reach the
// modem's TLS endpoint (such as fingerprint). It validates the modem host and
// timeout only: credentials, backends, and runtime settings are not required.
func LoadModemConfig(path string) (*Config, error) {
	cfg, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	var errs monitor.ValidationErrors
	validateModemEndpoint(cfg, &errs)
	if len(errs) > 0 {
		return nil, errs
	}
	return cfg, nil
}

// readConfig parses YAML, applies defaults and environment overrides, and
// normalizes modem settings that later validation checks.
func readConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Config{
		Modem: ModemConfig{
			LockoutBackoff: defaultLockoutBackoff,
			Timeout:        defaultModemTimeout,
		},
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := applyEnvOverrides(&cfg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Modem.EventTimezone) == "" {
		cfg.Modem.EventTimezone = defaultEventTimezone
	}
	cfg.Modem.EventTimezone = strings.TrimSpace(cfg.Modem.EventTimezone)
	return &cfg, nil
}

func applyEnvOverrides(cfg *Config) error {
	textOverrides := []struct {
		name   string
		target *string
	}{
		{"MB8611_MODEM_HOST", &cfg.Modem.Host},
		{"MB8611_MODEM_USERNAME", &cfg.Modem.Username},
		{"MB8611_MODEM_PASSWORD", &cfg.Modem.Password},
		{"MB8611_MODEM_TLS_FINGERPRINT", &cfg.Modem.TLSFingerprintSHA256},
		{"MB8611_MODEM_EVENT_TIMEZONE", &cfg.Modem.EventTimezone},
		{"MB8611_INFLUXDB_URL", &cfg.InfluxDB.URL},
		{"MB8611_INFLUXDB_TOKEN", &cfg.InfluxDB.Token},
		{"MB8611_INFLUXDB_ORG", &cfg.InfluxDB.Org},
		{"MB8611_INFLUXDB_BUCKET", &cfg.InfluxDB.Bucket},
		{"MB8611_ES_URL", &cfg.Elasticsearch.URL},
		{"MB8611_ES_INDEX", &cfg.Elasticsearch.Index},
		{"MB8611_ES_API_KEY", &cfg.Elasticsearch.APIKey},
		{"MB8611_ES_USERNAME", &cfg.Elasticsearch.Username},
		{"MB8611_ES_PASSWORD", &cfg.Elasticsearch.Password},
		{"MB8611_HOSTNAME", &cfg.Hostname},
		{"MB8611_LOG_LEVEL", &cfg.LogLevel},
	}
	for _, override := range textOverrides {
		if value, ok := os.LookupEnv(override.name); ok && value != "" {
			*override.target = value
		}
	}

	boolOverrides := []struct {
		name   string
		target *bool
	}{
		{"MB8611_MODEM_INSECURE_TLS", &cfg.Modem.InsecureTLS},
		{"MB8611_INFLUXDB_ENABLED", &cfg.InfluxDB.Enabled},
		{"MB8611_ES_ENABLED", &cfg.Elasticsearch.Enabled},
	}
	for _, override := range boolOverrides {
		if value, ok := os.LookupEnv(override.name); ok && value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("environment %s: invalid boolean %q (use true or false)", override.name, value)
			}
			*override.target = parsed
		}
	}

	if value, ok := os.LookupEnv("MB8611_INTERVAL"); ok && value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("environment MB8611_INTERVAL: invalid duration %q: %w", value, err)
		}
		cfg.Interval = interval
	}
	return nil
}

func validate(cfg *Config, validateBackends bool) error {
	var errs monitor.ValidationErrors

	validateModemEndpoint(cfg, &errs)
	if strings.TrimSpace(cfg.Modem.Username) == "" {
		errs = append(errs, monitor.ValidationError{Field: "modem.username", Message: "must not be empty"})
	}
	if strings.TrimSpace(cfg.Modem.Password) == "" {
		errs = append(errs, monitor.ValidationError{Field: "modem.password", Message: "must not be empty"})
	}
	location, err := time.LoadLocation(cfg.Modem.EventTimezone)
	if err != nil {
		errs = append(errs, monitor.ValidationError{Field: "modem.event_timezone", Message: fmt.Sprintf("must be an IANA time zone name such as UTC or America/Chicago: %v", err)})
	} else {
		cfg.Modem.EventLocation = location
	}
	if strings.TrimSpace(cfg.Modem.TLSFingerprintSHA256) != "" {
		fingerprint, err := hnap.NormalizeFingerprint(cfg.Modem.TLSFingerprintSHA256)
		if err != nil {
			errs = append(errs, monitor.ValidationError{Field: "modem.tls_fingerprint_sha256", Message: err.Error()})
		} else {
			cfg.Modem.TLSFingerprintSHA256 = fingerprint
		}
	} else {
		cfg.Modem.TLSFingerprintSHA256 = ""
	}
	if cfg.Modem.LockoutBackoff <= 0 {
		errs = append(errs, monitor.ValidationError{Field: "modem.lockout_backoff", Message: "must be greater than zero"})
	}
	if validateBackends && cfg.InfluxDB.Enabled {
		if strings.TrimSpace(cfg.InfluxDB.URL) == "" {
			errs = append(errs, monitor.ValidationError{Field: "influxdb.url", Message: "must not be empty when enabled"})
		}
		if strings.TrimSpace(cfg.InfluxDB.Token) == "" {
			errs = append(errs, monitor.ValidationError{Field: "influxdb.token", Message: "must not be empty when enabled"})
		}
		if strings.TrimSpace(cfg.InfluxDB.Org) == "" {
			errs = append(errs, monitor.ValidationError{Field: "influxdb.org", Message: "must not be empty when enabled"})
		}
		if strings.TrimSpace(cfg.InfluxDB.Bucket) == "" {
			errs = append(errs, monitor.ValidationError{Field: "influxdb.bucket", Message: "must not be empty when enabled"})
		}
	}
	if validateBackends && cfg.Elasticsearch.Enabled {
		if strings.TrimSpace(cfg.Elasticsearch.URL) == "" {
			errs = append(errs, monitor.ValidationError{Field: "elasticsearch.url", Message: "must not be empty when enabled"})
		}
		if strings.TrimSpace(cfg.Elasticsearch.Index) == "" {
			errs = append(errs, monitor.ValidationError{Field: "elasticsearch.index", Message: "must not be empty when enabled"})
		}
		if strings.TrimSpace(cfg.Elasticsearch.APIKey) == "" && (strings.TrimSpace(cfg.Elasticsearch.Username) == "" || strings.TrimSpace(cfg.Elasticsearch.Password) == "") {
			errs = append(errs, monitor.ValidationError{Field: "elasticsearch.authentication", Message: "requires api_key or username and password when enabled"})
		}
	}
	if cfg.Interval <= 0 {
		errs = append(errs, monitor.ValidationError{Field: "interval", Message: "must be greater than zero"})
	}
	if !validLogLevel(cfg.LogLevel) {
		errs = append(errs, monitor.ValidationError{Field: "log_level", Message: "must be one of debug, info, warn, error"})
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// validateModemEndpoint checks and normalizes the settings needed to reach
// the modem at all: its host and the request timeout.
func validateModemEndpoint(cfg *Config, errs *monitor.ValidationErrors) {
	if strings.TrimSpace(cfg.Modem.Host) == "" {
		*errs = append(*errs, monitor.ValidationError{Field: "modem.host", Message: "must not be empty"})
	} else if host, err := hnap.NormalizeHost(cfg.Modem.Host); err != nil {
		*errs = append(*errs, monitor.ValidationError{Field: "modem.host", Message: err.Error()})
	} else {
		cfg.Modem.Host = host
	}
	if cfg.Modem.Timeout <= 0 {
		*errs = append(*errs, monitor.ValidationError{Field: "modem.timeout", Message: "must be greater than zero"})
	}
}

func validLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

// MonitorConfig converts Config to go-monitor's common monitor configuration.
func (cfg *Config) MonitorConfig() *monitor.Config {
	monitorConfig := monitor.DefaultConfig()
	monitorConfig.Global.PollInterval = monitor.Duration{Duration: cfg.Interval}
	monitorConfig.Global.LogLevel = cfg.LogLevel
	monitorConfig.Global.BatchSize = batchSize
	monitorConfig.InfluxDB = cfg.InfluxBackendConfig()
	return monitorConfig
}

// InfluxBackendConfig converts Config to go-monitor's InfluxDB configuration.
func (cfg *Config) InfluxBackendConfig() monitor.InfluxDBConfig {
	return monitor.InfluxDBConfig{
		Enabled: cfg.InfluxDB.Enabled,
		URL:     cfg.InfluxDB.URL,
		Token:   cfg.InfluxDB.Token,
		Org:     cfg.InfluxDB.Org,
		Bucket:  cfg.InfluxDB.Bucket,
	}
}

// ESBackendConfig converts Config to the go-monitor Elasticsearch configuration.
func (cfg *Config) ESBackendConfig() elasticsearch.Config {
	return elasticsearch.Config{
		Enabled:     cfg.Elasticsearch.Enabled,
		URL:         cfg.Elasticsearch.URL,
		Index:       cfg.Elasticsearch.Index,
		Username:    cfg.Elasticsearch.Username,
		Password:    cfg.Elasticsearch.Password,
		APIKey:      cfg.Elasticsearch.APIKey,
		InsecureTLS: cfg.Elasticsearch.InsecureTLS,
		IDFromTag:   cfg.Elasticsearch.IDFromTag,
	}
}
