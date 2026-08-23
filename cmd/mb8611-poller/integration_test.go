package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/danweinerdev/mb8611-poller/internal/docsis"
	"github.com/danweinerdev/mb8611-poller/internal/hnap/hnaptest"
)

func TestRunEchoOnceIntegration(t *testing.T) {
	responses := fixtureResponses(t)
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{
		Username:  "admin",
		Password:  "test",
		Responses: responses,
	})
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := fmt.Sprintf(`modem:
  host: %q
  username: admin
  password: test
  insecure_tls: true
interval: 1m
log_level: info
influxdb:
  enabled: true
elasticsearch:
  enabled: true
`, serverURL.Host)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	var first bytes.Buffer
	if got := run([]string{"--config", configPath, "--echo", "--run-once"}, &first); got != 0 {
		t.Fatalf("first run() = %d, output:\n%s", got, first.String())
	}
	var logResponse struct {
		Entries string `json:"MotoStatusLogList"`
	}
	if err := json.Unmarshal(responses["GetMotoStatusLog"], &logResponse); err != nil {
		t.Fatal(err)
	}
	for measurement, want := range map[string]int{
		"docsis_downstream": 34,
		"docsis_upstream":   5,
		"docsis_modem":      1,
		"docsis_event":      len(docsis.ParseEventLog(logResponse.Entries)),
	} {
		if got := measurementCount(first.String(), measurement); got != want {
			t.Errorf("first output %s count = %d, want %d", measurement, got, want)
		}
	}
	firstDocIDs := eventDocIDsFromOutput(t, first.String())
	if len(firstDocIDs) == 0 {
		t.Fatalf("first output contains no docsis_event doc_id tags:\n%s", first.String())
	}

	var second bytes.Buffer
	if got := run([]string{"--config", configPath, "--echo", "--run-once"}, &second); got != 0 {
		t.Fatalf("second run() = %d, output:\n%s", got, second.String())
	}
	secondDocIDs := eventDocIDsFromOutput(t, second.String())
	if !reflect.DeepEqual(secondDocIDs, firstDocIDs) {
		t.Errorf("docsis_event doc_id tags differ between runs:\nfirst:  %v\nsecond: %v", firstDocIDs, secondDocIDs)
	}
}

func measurementCount(output, measurement string) int {
	count := 0
	prefix := measurement + ","
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}

func eventDocIDsFromOutput(t *testing.T, output string) []string {
	t.Helper()

	var ids []string
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "docsis_event,") {
			continue
		}
		docID := ""
		for _, tag := range strings.Split(line, ",") {
			key, value, ok := strings.Cut(tag, "=")
			if ok && key == "doc_id" {
				docID, _, _ = strings.Cut(value, " ")
				break
			}
		}
		if docID == "" {
			t.Fatalf("docsis_event line missing doc_id tag: %q", line)
		}
		ids = append(ids, docID)
	}
	sort.Strings(ids)
	return ids
}
