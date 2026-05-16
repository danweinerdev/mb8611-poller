// Command mb8611-poller polls status from a Motorola MB8611 cable modem and
// publishes the collected metrics through go-monitor.
//
// This is the bootstrap stub from task 2.2: the CLI surface and go-monitor
// wiring are in place, but the collector returns no metrics yet. Real
// collection and backends arrive in later phases.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	monitor "github.com/danweinerdev/go-monitor"
)

func main() {
	fs := flag.NewFlagSet("mb8611-poller", flag.ExitOnError)
	configPath := fs.String("config", "config.yaml", "path to config file")
	runOnce := fs.Bool("run-once", false, "collect once and exit")
	// --echo is accepted so the documented CLI surface exists, but echo is
	// wired unconditionally below (this stub has no real backends until
	// Phase 6, and go-monitor requires at least one backend), so the flag
	// does not currently toggle echo on or off.
	_ = fs.Bool("echo", false, "print metrics to stdout")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}
	_ = configPath // parsed but unused until config loading lands in Phase 6.

	logger, _ := monitor.NewLogger("info")
	slog.SetDefault(logger)

	// Stub collector: no real backends exist yet, so nothing is collected.
	collectFunc := func(ctx context.Context) ([]*monitor.Metric, error) {
		return []*monitor.Metric{}, nil
	}

	opts := []monitor.Option{
		monitor.WithConfig(monitor.DefaultConfig()),
		monitor.WithLogger(logger),
		monitor.WithRunOnce(*runOnce),
		// Echo is wired unconditionally until Phase 6 adds real backends:
		// go-monitor errors with "no backends configured" otherwise.
		monitor.WithEcho(true),
	}

	m, err := monitor.New("mb8611-poller", collectFunc, opts...)
	if err != nil {
		slog.Error("failed to initialize monitor", "error", err)
		os.Exit(1)
	}

	if err := m.Run(context.Background()); err != nil {
		slog.Error("monitor exited with error", "error", err)
		os.Exit(1)
	}
}
