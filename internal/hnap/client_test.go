package hnap_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/danweinerdev/mb8611-poller/internal/hnap"
	"github.com/danweinerdev/mb8611-poller/internal/hnap/hnaptest"
)

func TestClientLoginAndMulti(t *testing.T) {
	responses := fixtureResponses(t)
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret", Responses: responses})
	client := hnap.New(hnap.Config{Host: testHost(t, server.URL), Username: "admin", Password: "secret", InsecureTLS: true})
	if err := client.EnsureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := hnaptest.Stats(server).LoginSuccesses; got != 1 {
		t.Fatalf("login successes = %d", got)
	}
	if err := client.EnsureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := hnaptest.Stats(server).LoginSuccesses; got != 1 {
		t.Fatalf("duplicate login successes = %d", got)
	}
	result, err := client.Multi(context.Background(), fixtureActions...)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range fixtureActions {
		if got := result[action+"Response"]; len(got) == 0 {
			t.Fatalf("missing %s response", action)
		}
	}
}

var fixtureActions = []string{
	"GetMotoStatusDownstreamChannelInfo", "GetMotoStatusUpstreamChannelInfo",
	"GetMotoStatusConnectionInfo", "GetMotoStatusSoftware",
	"GetMotoStatusStartupSequence", "GetMotoStatusLog",
}

func fixtureResponses(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	responses := make(map[string]json.RawMessage, len(fixtureActions))
	for _, action := range fixtureActions {
		contents, err := os.ReadFile(filepath.Join("hnaptest", "testdata", action+".json"))
		if err != nil {
			t.Fatal(err)
		}
		responses[action] = contents
	}
	return responses
}

func TestClientLoginFailureModes(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret", FailedLoginsBeforeLockout: 1})
	client := hnap.New(hnap.Config{Host: testHost(t, server.URL), Username: "admin", Password: "wrong", InsecureTLS: true})
	if err := client.Login(context.Background()); err != hnap.ErrAuthFailed {
		t.Fatalf("bad password = %v", err)
	}
	if err := client.Login(context.Background()); err != hnap.ErrLockedOut {
		t.Fatalf("lockout = %v", err)
	}
}

func TestClientRetriesOnceAfterUnauth(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret", ForceUnauthAfter: 1})
	client := hnap.New(hnap.Config{Host: testHost(t, server.URL), Username: "admin", Password: "secret", InsecureTLS: true})
	if err := client.EnsureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Call(context.Background(), "GetMotoStatusLog", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	stats := hnaptest.Stats(server)
	if stats.LoginSuccesses != 2 || stats.Calls["GetMotoStatusLog"] != 2 {
		t.Fatalf("retry stats = %+v", stats)
	}
	if stats.LastSOAPAction != `"http://purenetworks.com/HNAP1/GetMotoStatusLog"` || stats.LastHNAPAuth == "" {
		t.Fatalf("missing auth headers: %+v", stats)
	}
}

func TestClientUsesHNAP1AndLogoutResetsSession(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret", PathPrefix: "/HNAP/"})
	client := hnap.New(hnap.Config{Host: testHost(t, server.URL), Username: "admin", Password: "secret", InsecureTLS: true})
	if err := client.EnsureLogin(context.Background()); err == nil {
		t.Fatal("EnsureLogin unexpectedly accepted /HNAP1/ against a /HNAP/ server")
	}

	server = hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret"})
	client = hnap.New(hnap.Config{Host: testHost(t, server.URL), Username: "admin", Password: "secret", InsecureTLS: true})
	if err := client.EnsureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := hnaptest.Stats(server).LoginSuccesses; got != 2 {
		t.Fatalf("login successes after logout = %d", got)
	}
}

func testHost(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Host
}
