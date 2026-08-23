// Package config loads and validates the mb8611-poller configuration.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	monitor "github.com/danweinerdev/go-monitor"
	"github.com/danweinerdev/go-monitor/elasticsearch"
	"gopkg.in/yaml.v3"
)

const (
	defaultLockoutBackoff = 6 * time.Minute
)

var (
	defaultInfluxDBInclude      = []string{"docsis_downstream", "docsis_upstream", "docsis_modem"}
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Config{
		Modem: ModemConfig{
			InsecureTLS:    true,
			LockoutBackoff: defaultLockoutBackoff,
		},
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyEnvOverrides(&cfg)
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

	if err := validate(&cfg, validateBackends); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if value, ok := os.LookupEnv("MB8611_MODEM_HOST"); ok && value != "" {
		cfg.Modem.Host = value
	}
	if value, ok := os.LookupEnv("MB8611_MODEM_USERNAME"); ok && value != "" {
		cfg.Modem.Username = value
	}
	if value, ok := os.LookupEnv("MB8611_MODEM_PASSWORD"); ok && value != "" {
		cfg.Modem.Password = value
	}
	if value, ok := os.LookupEnv("MB8611_INFLUXDB_URL"); ok && value != "" {
		cfg.InfluxDB.URL = value
	}
	if value, ok := os.LookupEnv("MB8611_INFLUXDB_TOKEN"); ok && value != "" {
		cfg.InfluxDB.Token = value
	}
	if value, ok := os.LookupEnv("MB8611_INFLUXDB_ORG"); ok && value != "" {
		cfg.InfluxDB.Org = value
	}
	if value, ok := os.LookupEnv("MB8611_INFLUXDB_BUCKET"); ok && value != "" {
		cfg.InfluxDB.Bucket = value
	}
	if value, ok := os.LookupEnv("MB8611_ES_URL"); ok && value != "" {
		cfg.Elasticsearch.URL = value
	}
	if value, ok := os.LookupEnv("MB8611_ES_INDEX"); ok && value != "" {
		cfg.Elasticsearch.Index = value
	}
	if value, ok := os.LookupEnv("MB8611_ES_API_KEY"); ok && value != "" {
		cfg.Elasticsearch.APIKey = value
	}
	if value, ok := os.LookupEnv("MB8611_ES_USERNAME"); ok && value != "" {
		cfg.Elasticsearch.Username = value
	}
	if value, ok := os.LookupEnv("MB8611_ES_PASSWORD"); ok && value != "" {
		cfg.Elasticsearch.Password = value
	}
	if value, ok := os.LookupEnv("MB8611_INTERVAL"); ok && value != "" {
		if interval, err := time.ParseDuration(value); err == nil {
			cfg.Interval = interval
		}
	}
	if value, ok := os.LookupEnv("MB8611_HOSTNAME"); ok && value != "" {
		cfg.Hostname = value
	}
	if value, ok := os.LookupEnv("MB8611_LOG_LEVEL"); ok && value != "" {
		cfg.LogLevel = value
	}
}

func validate(cfg *Config, validateBackends bool) error {
	var errs monitor.ValidationErrors

	if strings.TrimSpace(cfg.Modem.Host) == "" {
		errs = append(errs, monitor.ValidationError{Field: "modem.host", Message: "must not be empty"})
	}
	if strings.TrimSpace(cfg.Modem.Password) == "" {
		errs = append(errs, monitor.ValidationError{Field: "modem.password", Message: "must not be empty"})
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
