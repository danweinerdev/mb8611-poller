// Package hnaptest provides protocol-level test doubles for HNAP clients.
package hnaptest

import (
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- HNAP requires HMAC-MD5.
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	publicKey = "HNAPTESTPUBLIC"
	challenge = "HNAPTESTCHALLENGE"
)

// ServerConfig controls the behavior of a fake HNAP modem.
type ServerConfig struct {
	Username                  string
	Password                  string
	Responses                 map[string]json.RawMessage
	FailedLoginsBeforeLockout int
	ForceUnauthAfter          int
	PathPrefix                string
}

// ServerStats is a concurrency-safe snapshot returned by Stats.
type ServerStats struct {
	LoginAttempts  int
	LoginSuccesses int
	Calls          map[string]int
	Locked         bool
	LastSOAPAction string
	LastHNAPAuth   string
}

type serverState struct {
	mu sync.Mutex

	cfg              ServerConfig
	privateKey       string
	failedLogins     int
	challengeIssued  bool
	authenticated    bool
	authenticatedOps int
	stats            ServerStats
}

var serverStates sync.Map // map[*httptest.Server]*serverState

// NewServer starts a TLS HNAP endpoint. Callers must Close the returned server.
func NewServer(t *testing.T, cfg ServerConfig) *httptest.Server {
	t.Helper()
	if cfg.PathPrefix == "" {
		cfg.PathPrefix = "/HNAP1/"
	}
	if cfg.Username == "" {
		cfg.Username = "admin"
	}
	responses := make(map[string]json.RawMessage, len(cfg.Responses))
	for action, body := range cfg.Responses {
		if !json.Valid(body) {
			t.Fatalf("hnaptest: response for %q is not valid JSON", action)
		}
		responses[action] = append(json.RawMessage(nil), body...)
	}
	cfg.Responses = responses

	state := &serverState{
		cfg:        cfg,
		privateKey: hmacMD5(publicKey+cfg.Password, challenge),
		stats: ServerStats{
			Calls: make(map[string]int),
		},
	}

	server := httptest.NewTLSServer(http.HandlerFunc(state.serveHTTP))
	serverStates.Store(server, state)
	t.Cleanup(func() {
		serverStates.Delete(server)
		server.Close()
	})
	return server
}

// Stats returns a point-in-time snapshot of server activity.
func Stats(server *httptest.Server) ServerStats {
	value, ok := serverStates.Load(server)
	if !ok {
		return ServerStats{Calls: make(map[string]int)}
	}
	state := value.(*serverState)
	state.mu.Lock()
	defer state.mu.Unlock()

	stats := state.stats
	stats.Locked = state.locked()
	stats.Calls = make(map[string]int, len(state.stats.Calls))
	for action, count := range state.stats.Calls {
		stats.Calls[action] = count
	}
	return stats
}

func (s *serverState) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.cfg.PathPrefix {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if login, ok := request["Login"]; ok {
		s.login(w, login)
		return
	}

	s.call(w, r, request)
}

func (s *serverState) login(w http.ResponseWriter, raw json.RawMessage) {
	var login struct {
		Username      string
		LoginPassword string
		PrivateLogin  string
	}
	if err := json.Unmarshal(raw, &login); err != nil {
		http.Error(w, "invalid login", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if login.LoginPassword == "" && login.PrivateLogin == "LoginPassword" {
		s.stats.LoginAttempts++
		if s.locked() {
			s.writeJSON(w, map[string]any{"LoginResponse": map[string]any{"LoginResult": "FAILED"}})
			return
		}
		s.challengeIssued = true
		w.Header().Set("Set-Cookie", "uid=hnaptest; Path=/")
		s.writeJSON(w, map[string]any{"LoginResponse": map[string]any{
			"LoginResult": "OK", "PublicKey": publicKey, "Challenge": challenge, "Cookie": "hnaptest",
		}})
		return
	}

	expected := hmacMD5(s.privateKey, challenge)
	if !s.locked() && s.challengeIssued && login.Username == s.cfg.Username && hmac.Equal([]byte(login.LoginPassword), []byte(expected)) {
		s.authenticated = true
		s.authenticatedOps = 0
		s.stats.LoginSuccesses++
		s.writeJSON(w, map[string]any{"LoginResponse": map[string]any{"LoginResult": "OK"}})
		return
	}

	s.failedLogins++
	s.challengeIssued = false
	s.writeJSON(w, map[string]any{"LoginResponse": map[string]any{"LoginResult": "FAILED"}})
}

func (s *serverState) call(w http.ResponseWriter, r *http.Request, request map[string]json.RawMessage) {
	action := actionFromSOAPAction(r.Header.Get("SOAPACTION"))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.LastSOAPAction = r.Header.Get("SOAPACTION")
	s.stats.LastHNAPAuth = r.Header.Get("HNAP_AUTH")
	if action == "" || !s.authenticated || !validHNAPAuth(r.Header.Get("HNAP_AUTH"), action, s.privateKey) {
		s.writeUnauth(w, action)
		return
	}

	s.authenticatedOps++
	s.stats.Calls[action]++
	if s.cfg.ForceUnauthAfter > 0 && s.authenticatedOps == s.cfg.ForceUnauthAfter {
		s.authenticated = false
		s.writeUnauth(w, action)
		return
	}
	if action != "GetMultipleHNAPs" {
		s.writeJSON(w, map[string]any{action + "Response": map[string]any{action + "Result": "OK"}})
		return
	}

	var multiple map[string]json.RawMessage
	if err := json.Unmarshal(request["GetMultipleHNAPs"], &multiple); err != nil {
		http.Error(w, "invalid GetMultipleHNAPs", http.StatusBadRequest)
		return
	}
	response := make(map[string]json.RawMessage, len(multiple))
	for requestedAction := range multiple {
		body, ok := s.cfg.Responses[requestedAction]
		if !ok {
			body = json.RawMessage(`{}`)
		}
		response[requestedAction+"Response"] = body
	}
	s.writeJSON(w, map[string]any{"GetMultipleHNAPsResponse": response})
}

func (s *serverState) locked() bool {
	return s.cfg.FailedLoginsBeforeLockout > 0 && s.failedLogins >= s.cfg.FailedLoginsBeforeLockout
}

func (s *serverState) writeUnauth(w http.ResponseWriter, action string) {
	if action == "" {
		action = "Unknown"
	}
	s.writeJSON(w, map[string]any{action + "Response": map[string]any{action + "Result": "UN-AUTH"}})
}

func (s *serverState) writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func actionFromSOAPAction(value string) string {
	const prefix = `"http://purenetworks.com/HNAP1/`
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, `"`) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(value, prefix), `"`)
}

func validHNAPAuth(value, action, privateKey string) bool {
	parts := strings.Split(value, " ")
	if len(parts) != 2 {
		return false
	}
	expected := hmacMD5(privateKey, parts[1]+`"http://purenetworks.com/HNAP1/`+action+`"`)
	return hmac.Equal([]byte(parts[0]), []byte(expected))
}

func hmacMD5(key, message string) string {
	mac := hmac.New(md5.New, []byte(key)) // #nosec G401 -- HNAP requires HMAC-MD5.
	_, _ = mac.Write([]byte(message))
	return fmt.Sprintf("%X", mac.Sum(nil))
}
