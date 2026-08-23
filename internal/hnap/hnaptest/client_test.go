package hnaptest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/danweinerdev/mb8611-poller/internal/hnap"
)

func TestMockClientLoginAndLockout(t *testing.T) {
	mock := NewMockClient()
	if err := mock.EnsureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := mock.EnsureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mock.LoginCount != 1 {
		t.Fatalf("login count = %d", mock.LoginCount)
	}
	if err := mock.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if mock.LoginCount != 2 {
		t.Fatalf("forced login count = %d", mock.LoginCount)
	}
	mock.LockedOut = true
	if err := mock.EnsureLogin(context.Background()); !errors.Is(err, hnap.ErrLockedOut) {
		t.Fatalf("lockout error = %v", err)
	}
}

func TestMockClientBuildersAndMulti(t *testing.T) {
	fixture := filepath.Join("testdata", "mock.json")
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(fixture); os.Remove("testdata") })
	if err := os.WriteFile(fixture, []byte(`{"fixture":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	mock := NewMockClient().WithResponse("A", json.RawMessage(`{"a":1}`)).WithFixture("B", "mock.json").WithError("C", boom)
	responses, err := mock.Multi(context.Background(), "A", "B")
	if err != nil {
		t.Fatal(err)
	}
	if string(responses["AResponse"]) != `{"a":1}` || string(responses["BResponse"]) != `{"fixture":true}` {
		t.Fatalf("responses = %v", responses)
	}
	if got := mock.LastMultiArgs; len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("multi args = %v", got)
	}
	if _, err := mock.Call(context.Background(), "C", nil); !errors.Is(err, boom) {
		t.Fatalf("call error = %v", err)
	}
	if _, err := mock.Call(context.Background(), "missing", nil); err == nil {
		t.Fatal("missing response succeeded")
	}
}
