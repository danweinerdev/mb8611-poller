package hnaptest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/danweinerdev/mb8611-poller/internal/hnap"
)

// MockClient is a deterministic hnap.Client implementation for collector tests.
type MockClient struct {
	Responses     map[string]json.RawMessage
	Errors        map[string]error
	LoginErr      error
	LockedOut     bool
	LoginCount    int
	CallCounts    map[string]int
	LastMultiArgs []string
	loggedIn      bool
}

var _ hnap.Client = (*MockClient)(nil)

func NewMockClient() *MockClient {
	return &MockClient{Responses: make(map[string]json.RawMessage), Errors: make(map[string]error), CallCounts: make(map[string]int)}
}

func (m *MockClient) EnsureLogin(context.Context) error {
	if m.LockedOut {
		return hnap.ErrLockedOut
	}
	if m.LoginErr != nil {
		return m.LoginErr
	}
	if !m.loggedIn {
		m.LoginCount++
		m.loggedIn = true
	}
	return nil
}

func (m *MockClient) Login(context.Context) error {
	if m.LockedOut {
		return hnap.ErrLockedOut
	}
	if m.LoginErr != nil {
		return m.LoginErr
	}
	m.LoginCount++
	m.loggedIn = true
	return nil
}

func (m *MockClient) Call(_ context.Context, action string, _ any) (json.RawMessage, error) {
	if m.CallCounts == nil {
		m.CallCounts = make(map[string]int)
	}
	m.CallCounts[action]++
	if err, ok := m.Errors[action]; ok {
		return nil, err
	}
	response, ok := m.Responses[action]
	if !ok {
		return nil, fmt.Errorf("hnaptest: no response for %q", action)
	}
	return append(json.RawMessage(nil), response...), nil
}

func (m *MockClient) Multi(ctx context.Context, actions ...string) (map[string]json.RawMessage, error) {
	m.LastMultiArgs = append([]string(nil), actions...)
	responses := make(map[string]json.RawMessage, len(actions))
	for _, action := range actions {
		response, err := m.Call(ctx, action, nil)
		if err != nil {
			return nil, err
		}
		responses[action+"Response"] = response
	}
	return responses, nil
}

func (m *MockClient) Logout(context.Context) error { m.loggedIn = false; return nil }

func (m *MockClient) WithResponse(action string, response json.RawMessage) *MockClient {
	if m.Responses == nil {
		m.Responses = make(map[string]json.RawMessage)
	}
	m.Responses[action] = append(json.RawMessage(nil), response...)
	return m
}

func (m *MockClient) WithFixture(action, relpath string) *MockClient {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		panic("hnaptest: determine fixture directory")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(source), "testdata", relpath))
	if err != nil {
		panic(fmt.Sprintf("hnaptest: read fixture %q: %v", relpath, err))
	}
	return m.WithResponse(action, contents)
}

func (m *MockClient) WithError(action string, err error) *MockClient {
	if m.Errors == nil {
		m.Errors = make(map[string]error)
	}
	m.Errors[action] = err
	return m
}
