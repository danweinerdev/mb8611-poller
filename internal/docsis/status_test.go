package docsis

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseConnectionInfoFixture(t *testing.T) {
	info, err := ParseConnectionInfo(statusFixture(t, "GetMotoStatusConnectionInfo.json"))
	if err != nil {
		t.Fatal(err)
	}

	if want := (ConnectionInfo{
		NetworkAccess: "Allowed",
		SystemUpTime:  "45 days 03h:09m:15s",
	}); info != want {
		t.Errorf("connection info = %+v, want %+v", info, want)
	}

	if got, ok := info.Uptime(); !ok || got != 1083*time.Hour+9*time.Minute+15*time.Second {
		t.Errorf("Uptime() = (%v, %t), want (%v, true)", got, ok, 1083*time.Hour+9*time.Minute+15*time.Second)
	}
}

func TestConnectionInfoUptimeInvalid(t *testing.T) {
	if got, ok := (ConnectionInfo{SystemUpTime: "unknown"}).Uptime(); ok || got != 0 {
		t.Errorf("Uptime() = (%v, %t), want (0, false)", got, ok)
	}
}

func TestParseSoftwareFixture(t *testing.T) {
	software, err := ParseSoftware(statusFixture(t, "GetMotoStatusSoftware.json"))
	if err != nil {
		t.Fatal(err)
	}

	if want := (Software{
		SpecVer:     "DOCSIS 3.1",
		HdVer:       "V1.0",
		SfVer:       "8611-19.2.18",
		MacAddress:  "XX:XX:XX:XX:XX:XX",
		SerialNum:   "REDACTED",
		Certificate: "Installed",
		CustomerVer: "REDACTED",
	}); software != want {
		t.Errorf("software = %+v, want %+v", software, want)
	}
}

func TestParseStartupSequenceFixture(t *testing.T) {
	startup, err := ParseStartupSequence(statusFixture(t, "GetMotoStatusStartupSequence.json"))
	if err != nil {
		t.Fatal(err)
	}

	if want := (StartupSequence{
		BootStatus:               "OK",
		BootComment:              "Operational",
		ConfigurationFileStatus:  "OK",
		ConfigurationFileComment: "",
		ConnectivityStatus:       "OK",
		ConnectivityComment:      "Operational",
		DSFreq:                   "237000000 Hz",
		DSComment:                "Locked",
		SecurityStatus:           "Enabled",
		SecurityComment:          "BPI+",
	}); startup != want {
		t.Errorf("startup sequence = %+v, want %+v", startup, want)
	}
}

func TestParseStatusInvalidJSON(t *testing.T) {
	parsers := map[string]func(json.RawMessage) error{
		"connection info": func(raw json.RawMessage) error {
			_, err := ParseConnectionInfo(raw)
			return err
		},
		"software": func(raw json.RawMessage) error {
			_, err := ParseSoftware(raw)
			return err
		},
		"startup sequence": func(raw json.RawMessage) error {
			_, err := ParseStartupSequence(raw)
			return err
		},
	}

	for name, parse := range parsers {
		t.Run(name, func(t *testing.T) {
			err := parse(json.RawMessage(`{`))
			if err == nil {
				t.Fatal("parse error = nil, want wrapped JSON error")
			}

			var syntaxError *json.SyntaxError
			if !errors.As(err, &syntaxError) {
				t.Errorf("error = %v, want wrapped *json.SyntaxError", err)
			}
		})
	}
}

func statusFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join("..", "hnap", "hnaptest", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(contents)
}
