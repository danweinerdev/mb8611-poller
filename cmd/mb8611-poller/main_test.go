package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		"interval: 1m\n" +
		"log_level: info\n" +
		"influxdb:\n" +
		"  enabled: true\n" +
		"elasticsearch:\n" +
		"  enabled: true\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if got := run([]string{"--config", configPath, "--echo", "--run-once"}, &output); got != 0 {
		t.Fatalf("run() = %d, output: %s", got, output.String())
	}
	for _, measurement := range []string{"docsis_downstream,", "docsis_upstream,", "docsis_modem,", "docsis_event,"} {
		if !strings.Contains(output.String(), measurement) {
			t.Errorf("echo output missing %q:\n%s", measurement, output.String())
		}
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
