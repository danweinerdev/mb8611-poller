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
	"os/signal"
	"syscall"
	"time"

	monitor "github.com/danweinerdev/go-monitor"
	"github.com/danweinerdev/go-monitor/elasticsearch"
	"github.com/danweinerdev/go-monitor/influxdb"
	"github.com/danweinerdev/mb8611-poller/internal/collector"
	"github.com/danweinerdev/mb8611-poller/internal/config"
	"github.com/danweinerdev/mb8611-poller/internal/hnap"
	"github.com/spf13/cobra"
)

const (
	// finalPollTimeout bounds the poll go-monitor runs after SIGTERM. With
	// go-monitor's 30s final flush and logoutTimeout, worst-case shutdown is
	// 45s, inside a Kubernetes terminationGracePeriodSeconds of 75.
	finalPollTimeout = 10 * time.Second
	// logoutTimeout bounds the best-effort HNAP logout on exit.
	logoutTimeout = 5 * time.Second
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
			return errors.New("a command is required; use \"run\", \"run-once\", or \"fingerprint\"")
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&options.configPath, "config", "config.yaml", "path to config file")
	root.PersistentFlags().BoolVar(&options.echo, "echo", false, "print metrics to stdout instead of publishing them")

	root.AddCommand(newMonitorCommand("run", "Start the polling loop", false, &options, stdout, stderr, runner))
	root.AddCommand(newMonitorCommand("run-once", "Collect metrics once and exit", true, &options, stdout, stderr, runner))
	root.AddCommand(newFingerprintCommand(&options, stdout, stderr))
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
	if cfg.Modem.TLSFingerprintSHA256 != "" && cfg.Modem.InsecureTLS {
		logger.Warn("modem.insecure_tls is ignored because modem.tls_fingerprint_sha256 is set; the pinned certificate is enforced")
	}
	client := hnap.New(hnap.Config{
		Host:                 cfg.Modem.Host,
		Username:             cfg.Modem.Username,
		Password:             cfg.Modem.Password,
		Timeout:              cfg.Modem.Timeout,
		InsecureTLS:          cfg.Modem.InsecureTLS,
		TLSFingerprintSHA256: cfg.Modem.TLSFingerprintSHA256,
	})
	modemCollector := collector.NewCollector(client, collector.Config{
		Host:           cfg.Hostname,
		ModemTag:       cfg.Modem.Host,
		LockoutBackoff: cfg.Modem.LockoutBackoff,
		EventLocation:  cfg.Modem.EventLocation,
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
	// go-monitor installs its own SIGINT/SIGTERM handler; this context sees
	// the same signals so the post-signal final poll can be bounded.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	collect := boundShutdownPoll(ctx, finalPollTimeout, collector.MakeCollectFunc(modemCollector))
	m, err := monitor.New("mb8611-poller", collect, monitorOptions...)
	if err != nil {
		fmt.Fprintf(stderr, "initialize monitor: %v\n", err)
		return 1
	}
	runErr := m.Run(ctx)
	logoutModem(client, logger)
	if runErr != nil {
		logger.Error("monitor exited with error", "error", runErr)
		fmt.Fprintf(stderr, "monitor: %v\n", runErr)
		return 1
	}
	return 0
}

// boundShutdownPoll caps the poll go-monitor runs after shutdown is signalled
// (it otherwise allows 30s, on top of a 30s final flush). Once running is
// done, each poll gets at most timeout.
func boundShutdownPoll(running context.Context, timeout time.Duration, collect monitor.CollectFunc) monitor.CollectFunc {
	return func(ctx context.Context) ([]*monitor.Metric, error) {
		if running.Err() != nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		return collect(ctx)
	}
}

// logoutModem releases the modem's single admin session on exit. It is best
// effort: failures are logged at debug level by the client.
func logoutModem(client hnap.Client, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), logoutTimeout)
	defer cancel()
	if err := client.Logout(ctx); err != nil {
		logger.Debug("modem logout failed", "error", err)
	}
}

func newFingerprintCommand(options *commandOptions, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "fingerprint",
		Short: "Print the SHA-256 fingerprint of the modem's TLS certificate",
		Long: "Connect to modem.host, complete a TLS handshake only (no login, no credentials needed),\n" +
			"and print the SHA-256 fingerprint of the leaf certificate for modem.tls_fingerprint_sha256.\n" +
			"Verify the value out of band before pinning it.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if code := runFingerprint(*options, stdout, stderr); code != 0 {
				return commandExitError{code: code}
			}
			return nil
		},
	}
}

func runFingerprint(options commandOptions, stdout, stderr io.Writer) int {
	cfg, err := config.LoadModemConfig(options.configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	fingerprint, cert, err := hnap.FetchLeafFingerprint(context.Background(), cfg.Modem.Host, cfg.Modem.Timeout)
	if err != nil {
		fmt.Fprintf(stderr, "fingerprint: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, fingerprint)
	fmt.Fprintf(stderr, "host: %s\nsubject: %s\nissuer: %s\nnot before: %s\nnot after: %s\n",
		cfg.Modem.Host, cert.Subject, cert.Issuer, cert.NotBefore.UTC().Format(time.RFC3339), cert.NotAfter.UTC().Format(time.RFC3339))
	if configured := cfg.Modem.TLSFingerprintSHA256; configured != "" {
		if pinned, err := hnap.NormalizeFingerprint(configured); err != nil {
			fmt.Fprintf(stderr, "configured modem.tls_fingerprint_sha256 is invalid: %v\n", err)
		} else if pinned == fingerprint {
			fmt.Fprintln(stderr, "configured modem.tls_fingerprint_sha256 matches")
		} else {
			fmt.Fprintln(stderr, "configured modem.tls_fingerprint_sha256 does not match")
		}
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
