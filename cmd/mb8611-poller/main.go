// Command mb8611-poller polls status from a Motorola MB8611 cable modem and
// publishes the collected metrics through go-monitor.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	monitor "github.com/danweinerdev/go-monitor"
	"github.com/danweinerdev/go-monitor/elasticsearch"
	"github.com/danweinerdev/go-monitor/influxdb"
	"github.com/danweinerdev/mb8611-poller/internal/collector"
	"github.com/danweinerdev/mb8611-poller/internal/config"
	"github.com/danweinerdev/mb8611-poller/internal/hnap"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

// run is the testable command entry point.
func run(args []string, stdout io.Writer) int {
	flags := flag.NewFlagSet("mb8611-poller", flag.ContinueOnError)
	flags.SetOutput(stdout)
	configPath := flags.String("config", "config.yaml", "path to config file")
	runOnce := flags.Bool("run-once", false, "collect once and exit")
	echo := flags.Bool("echo", false, "print metrics to stdout instead of publishing them")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	var (
		cfg *config.Config
		err error
	)
	if *echo {
		cfg, err = config.LoadConfigForEcho(*configPath)
	} else {
		cfg, err = config.LoadConfig(*configPath)
	}
	if err != nil {
		fmt.Fprintf(stdout, "load config: %v\n", err)
		return 1
	}

	logger, levelVar := monitor.NewLogger(cfg.LogLevel)
	client := hnap.New(hnap.Config{
		Host:        cfg.Modem.Host,
		Username:    cfg.Modem.Username,
		Password:    cfg.Modem.Password,
		InsecureTLS: cfg.Modem.InsecureTLS,
	})
	modemCollector := collector.NewCollector(client, collector.Config{
		Host:           cfg.Modem.Host,
		ModemTag:       cfg.Modem.Host,
		LockoutBackoff: cfg.Modem.LockoutBackoff,
	}, logger)

	influxBackend, elasticsearchBackend := buildBackends(cfg, *echo, stdout, logger)
	monitorConfig := cfg.MonitorConfig()
	if *runOnce {
		// Keep the one-shot batch in the pipeline until its synchronous final
		// flush. go-monitor v0.1.0 otherwise starts an untracked async flush
		// when the default batch size is reached and can return too early.
		monitorConfig.Global.BatchSize = 10_000
	}
	loadConfig := config.LoadConfig
	if *echo {
		loadConfig = config.LoadConfigForEcho
	}
	reloadFn := newReloadFunc(*configPath, loadConfig, levelVar)

	m, err := monitor.New("mb8611-poller", collector.MakeCollectFunc(modemCollector),
		monitor.WithConfig(monitorConfig),
		monitor.WithLogger(logger),
		monitor.WithRunOnce(*runOnce),
		monitor.WithBackend(influxBackend),
		monitor.WithBackend(elasticsearchBackend),
		monitor.WithReloadFunc(reloadFn),
	)
	if err != nil {
		fmt.Fprintf(stdout, "initialize monitor: %v\n", err)
		return 1
	}
	if err := m.Run(context.Background()); err != nil {
		logger.Error("monitor exited with error", "error", err)
		fmt.Fprintf(stdout, "monitor: %v\n", err)
		return 1
	}
	return 0
}

// newReloadFunc reloads runtime monitor settings from configPath. The path
// supplied by go-monitor is empty when WithConfig is used, so it must not be
// used here. Backend, HNAP, and routing objects are intentionally not rebuilt.
func newReloadFunc(configPath string, loadConfig func(string) (*config.Config, error), levelVar *slog.LevelVar) func(string) (*monitor.Config, error) {
	return func(string) (*monitor.Config, error) {
		cfg, err := loadConfig(configPath)
		if err != nil {
			return nil, err
		}
		levelVar.Set(monitor.ParseLogLevel(cfg.LogLevel))
		return cfg.MonitorConfig(), nil
	}
}

func buildBackends(cfg *config.Config, echo bool, stdout io.Writer, logger *slog.Logger) (monitor.Backend, monitor.Backend) {
	var influxInner, elasticsearchInner monitor.Backend
	if echo {
		influxInner = monitor.NewEcho(stdout, logger)
		elasticsearchInner = monitor.NewEcho(stdout, logger)
	} else {
		influxInner = influxdb.New(cfg.InfluxBackendConfig(), logger)
		elasticsearchInner = elasticsearch.New(cfg.ESBackendConfig(), logger)
	}
	return &monitor.MeasurementFilter{Inner: influxInner, Include: cfg.Routing.InfluxDBInclude},
		&monitor.MeasurementFilter{Inner: elasticsearchInner, Include: cfg.Routing.ElasticsearchInclude}
}
