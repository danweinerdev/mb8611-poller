package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	monitor "github.com/danweinerdev/go-monitor"
	"github.com/danweinerdev/mb8611-poller/internal/hnap/hnaptest"
)

func writeTestConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func echoConfig(host string) string {
	return "modem:\n" +
		"  host: " + host + "\n" +
		"  username: admin\n" +
		"  password: secret\n" +
		"  insecure_tls: true\n" +
		"  timeout: 2s\n" +
		"interval: 1m\n" +
		"hostname: poller-host\n" +
		"log_level: error\n"
}

func TestRunOnceLogsOutOfModem(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret", Responses: fixtureResponses(t)})
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if got := run([]string{"run-once", "--echo", "--config", writeTestConfig(t, echoConfig(serverURL.Host))}, &output, io.Discard); got != 0 {
		t.Fatalf("run() = %d", got)
	}
	stats := hnaptest.Stats(server)
	if stats.LoginSuccesses != 1 || stats.Logouts != 1 {
		t.Fatalf("login successes = %d, logouts = %d; want 1 and 1", stats.LoginSuccesses, stats.Logouts)
	}
	if !strings.Contains(output.String(), "docsis_poll,") || !strings.Contains(output.String(), "success=1i") {
		t.Errorf("echo output missing successful docsis_poll point:\n%s", output.String())
	}
}

func TestRunOnceUnreachableModemStillEmitsPollStatus(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if got := run([]string{"run-once", "--echo", "--config", writeTestConfig(t, echoConfig(address))}, &output, io.Discard); got != 0 {
		t.Fatalf("run() = %d", got)
	}
	var pollLines []string
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "docsis_poll,") {
			pollLines = append(pollLines, line)
		}
	}
	if len(pollLines) != 1 {
		t.Fatalf("docsis_poll lines = %d, want 1:\n%s", len(pollLines), output.String())
	}
	if line := pollLines[0]; !strings.Contains(line, "success=0i") || !strings.Contains(line, "connection refused") || !strings.Contains(line, "modem="+address) {
		t.Errorf("unreachable poll point = %q, want success=0i with the connection error", line)
	}
}

func TestFingerprintCommandHandshakeOnly(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret"})
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(server.Certificate().Raw)
	want := hex.EncodeToString(sum[:])

	// No credentials and no backends: fingerprint must not need them.
	configPath := writeTestConfig(t, "modem:\n  host: https://"+serverURL.Host+"\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"fingerprint", "--config", configPath}, &stdout, &stderr); got != 0 {
		t.Fatalf("run(fingerprint) = %d, stderr: %s", got, stderr.String())
	}
	if got := stdout.String(); got != want+"\n" {
		t.Fatalf("fingerprint stdout = %q, want %q", got, want+"\n")
	}
	if stats := hnaptest.Stats(server); stats.Requests != 0 || stats.LoginAttempts != 0 {
		t.Fatalf("fingerprint made HTTP requests: %+v", stats)
	}

	pinned := writeTestConfig(t, "modem:\n  host: "+serverURL.Host+"\n  tls_fingerprint_sha256: "+strings.Repeat("00", 32)+"\n")
	stdout.Reset()
	stderr.Reset()
	if got := run([]string{"fingerprint", "--config", pinned}, &stdout, &stderr); got != 0 {
		t.Fatalf("run(fingerprint) with mismatched pin = %d", got)
	}
	if !strings.Contains(stderr.String(), "does not match") {
		t.Errorf("stderr = %q, want a pin mismatch notice", stderr.String())
	}
}

func TestFingerprintCommandUnreachable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	var stdout, stderr bytes.Buffer
	if got := run([]string{"fingerprint", "--config", writeTestConfig(t, "modem:\n  host: "+address+"\n  timeout: 2s\n")}, &stdout, &stderr); got != 1 {
		t.Fatalf("run(fingerprint) = %d, want 1", got)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "fingerprint:") {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRootHelpListsFingerprint(t *testing.T) {
	var stdout bytes.Buffer
	if got := run([]string{"--help"}, &stdout, io.Discard); got != 0 {
		t.Fatalf("run(--help) = %d", got)
	}
	if !strings.Contains(stdout.String(), "fingerprint") {
		t.Fatalf("help does not list fingerprint:\n%s", stdout.String())
	}
}

func TestBoundShutdownPoll(t *testing.T) {
	var sawDeadline bool
	var remaining time.Duration
	inner := monitor.CollectFunc(func(ctx context.Context) ([]*monitor.Metric, error) {
		deadline, ok := ctx.Deadline()
		sawDeadline = ok
		remaining = time.Until(deadline)
		return nil, nil
	})

	running, stop := context.WithCancel(context.Background())
	defer stop()
	collect := boundShutdownPoll(running, 50*time.Millisecond, inner)
	if _, err := collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sawDeadline {
		t.Fatal("regular poll got a shutdown deadline")
	}

	stop()
	if _, err := collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !sawDeadline || remaining > 50*time.Millisecond {
		t.Fatalf("shutdown poll deadline = %t remaining %s, want at most 50ms", sawDeadline, remaining)
	}
}

func TestShutdownBudgetFitsGracePeriod(t *testing.T) {
	// go-monitor v0.1.0 bounds its final pipeline flush at 30s.
	const goMonitorFlushBound = 30 * time.Second
	if total := finalPollTimeout + goMonitorFlushBound + logoutTimeout; total >= 60*time.Second {
		t.Fatalf("worst-case shutdown = %s, want under 60s (terminationGracePeriodSeconds: 75)", total)
	}
}
