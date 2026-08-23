// Command mb8611-poller polls status from a Motorola MB8611 cable modem and
// publishes the collected metrics through go-monitor.
package main

import (
	"context"
	"errors"
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
	"github.com/spf13/cobra"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable command entry point.
func run(args []string, stdout, stderr io.Writer) int {
	return executeCommand(args, stdout, stderr, runMonitor)
}

type commandOptions struct {
	configPath string
	echo       bool
}

type monitorRunner func(commandOptions, bool, io.Writer, io.Writer) int

type commandExitError struct {
	code int
}

func (err commandExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", err.code)
}

// executeCommand keeps command parsing separate from monitor execution so the
// command tree can be exercised without starting a polling loop.
func executeCommand(args []string, stdout, stderr io.Writer, runner monitorRunner) int {
	cmd := newRootCommand(stdout, stderr, runner)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		var exitErr commandExitError
		if errors.As(err, &exitErr) {
			return exitErr.code
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer, runner monitorRunner) *cobra.Command {
	options := commandOptions{}
	root := &cobra.Command{
		Use:           "mb8611-poller",
		Short:         "Poll Motorola MB8611 modem status",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("a command is required; use \"run\" or \"run-once\"")
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&options.configPath, "config", "config.yaml", "path to config file")
	root.PersistentFlags().BoolVar(&options.echo, "echo", false, "print metrics to stdout instead of publishing them")

	root.AddCommand(newMonitorCommand("run", "Start the polling loop", false, &options, stdout, stderr, runner))
	root.AddCommand(newMonitorCommand("run-once", "Collect metrics once and exit", true, &options, stdout, stderr, runner))
	return root
}

func newMonitorCommand(use, short string, runOnce bool, options *commandOptions, stdout, stderr io.Writer, runner monitorRunner) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if code := runner(*options, runOnce, stdout, stderr); code != 0 {
				return commandExitError{code: code}
			}
			return nil
		},
	}
}

func runMonitor(options commandOptions, runOnce bool, stdout, stderr io.Writer) int {

	var (
		cfg *config.Config
		err error
	)
	if options.echo {
		cfg, err = config.LoadConfigForEcho(options.configPath)
	} else {
		cfg, err = config.LoadConfig(options.configPath)
	}
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}

	logger, levelVar := monitor.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	client := hnap.New(hnap.Config{
		Host:        cfg.Modem.Host,
		Username:    cfg.Modem.Username,
		Password:    cfg.Modem.Password,
		InsecureTLS: cfg.Modem.InsecureTLS,
	})
	modemCollector := collector.NewCollector(client, collector.Config{
		Host:           cfg.Hostname,
		ModemTag:       cfg.Modem.Host,
		LockoutBackoff: cfg.Modem.LockoutBackoff,
	}, logger)

	backends, err := buildBackends(cfg, options.echo, stdout, logger)
	if err != nil {
		fmt.Fprintf(stderr, "build backends: %v\n", err)
		return 1
	}
	monitorConfig := cfg.MonitorConfig()
	if runOnce {
		// Keep the one-shot batch in the pipeline until its synchronous final
		// flush. go-monitor v0.1.0 otherwise starts an untracked async flush
		// when the default batch size is reached and can return too early.
		monitorConfig.Global.BatchSize = 10_000
	}
	loadConfig := config.LoadConfig
	if options.echo {
		loadConfig = config.LoadConfigForEcho
	}
	reloadFn := newReloadFunc(options.configPath, loadConfig, levelVar)

	monitorOptions := []monitor.Option{
		monitor.WithConfig(monitorConfig),
		monitor.WithLogger(logger),
		monitor.WithRunOnce(runOnce),
		monitor.WithReloadFunc(reloadFn),
	}
	for _, backend := range backends {
		monitorOptions = append(monitorOptions, monitor.WithBackend(backend))
	}
	m, err := monitor.New("mb8611-poller", collector.MakeCollectFunc(modemCollector), monitorOptions...)
	if err != nil {
		fmt.Fprintf(stderr, "initialize monitor: %v\n", err)
		return 1
	}
	if err := m.Run(context.Background()); err != nil {
		logger.Error("monitor exited with error", "error", err)
		fmt.Fprintf(stderr, "monitor: %v\n", err)
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

func buildBackends(cfg *config.Config, echo bool, stdout io.Writer, logger *slog.Logger) ([]monitor.Backend, error) {
	var backends []monitor.Backend
	if echo {
		backends = append(backends,
			&monitor.MeasurementFilter{Inner: monitor.NewEcho(stdout, logger), Include: cfg.Routing.InfluxDBInclude},
			&monitor.MeasurementFilter{Inner: monitor.NewEcho(stdout, logger), Include: cfg.Routing.ElasticsearchInclude},
		)
	} else {
		if cfg.InfluxDB.Enabled {
			backends = append(backends, &monitor.MeasurementFilter{Inner: influxdb.New(cfg.InfluxBackendConfig(), logger), Include: cfg.Routing.InfluxDBInclude})
		}
		if cfg.Elasticsearch.Enabled {
			backends = append(backends, &monitor.MeasurementFilter{Inner: elasticsearch.New(cfg.ESBackendConfig(), logger), Include: cfg.Routing.ElasticsearchInclude})
		}
	}
	for _, backend := range backends {
		if err := backend.(*monitor.MeasurementFilter).Validate(); err != nil {
			return nil, err
		}
	}
	return backends, nil
}
