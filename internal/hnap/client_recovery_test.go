package hnap_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danweinerdev/mb8611-poller/internal/hnap"
	"github.com/danweinerdev/mb8611-poller/internal/hnap/hnaptest"
)

const logSOAPAction = `"http://purenetworks.com/HNAP1/GetMotoStatusLog"`

// failingServer returns a fake modem whose GetMotoStatusLog calls are answered
// by fail while armed is set, and a counter of requests to /login.html.
func failingServer(t *testing.T, fail func(http.ResponseWriter, *http.Request)) (*httptest.Server, *atomic.Bool, *atomic.Int32) {
	t.Helper()
	var armed atomic.Bool
	var loginPageHits atomic.Int32
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{
		Password:  "secret",
		Responses: fixtureResponses(t),
		Intercept: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/login.html" {
				loginPageHits.Add(1)
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<html><body>login</body></html>"))
				return true
			}
			if armed.Load() && r.Header.Get("SOAPACTION") == logSOAPAction {
				fail(w, r)
				return true
			}
			return false
		},
	})
	return server, &armed, &loginPageHits
}

func TestClientResetsSessionAfterCallFailure(t *testing.T) {
	tests := []struct {
		name       string
		fail       func(http.ResponseWriter, *http.Request)
		wantUnauth bool
	}{
		{name: "non-2xx status", fail: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "busy", http.StatusServiceUnavailable)
		}},
		{name: "non-JSON body", fail: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>session expired</html>"))
		}},
		{name: "transport error", fail: func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				panic(err)
			}
			_ = conn.Close()
		}},
		{name: "redirect to login page", wantUnauth: true, fail: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/login.html", http.StatusFound)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, armed, loginPageHits := failingServer(t, test.fail)
			client := hnap.New(hnap.Config{Host: testHost(t, server.URL), Username: "admin", Password: "secret", InsecureTLS: true})
			ctx := context.Background()
			if err := client.EnsureLogin(ctx); err != nil {
				t.Fatal(err)
			}

			armed.Store(true)
			_, err := client.Call(ctx, "GetMotoStatusLog", map[string]any{})
			if err == nil {
				t.Fatal("Call() error = nil, want failure")
			}
			if test.wantUnauth && !errors.Is(err, hnap.ErrUnauth) {
				t.Errorf("Call() error = %v, want ErrUnauth", err)
			}
			if got := loginPageHits.Load(); got != 0 {
				t.Errorf("client followed redirect to login page %d times", got)
			}

			armed.Store(false)
			if err := client.EnsureLogin(ctx); err != nil {
				t.Fatal(err)
			}
			if got := hnaptest.Stats(server).LoginSuccesses; got != 2 {
				t.Fatalf("login successes after failed call = %d, want 2 (session must be reset)", got)
			}
			if _, err := client.Call(ctx, "GetMotoStatusLog", map[string]any{}); err != nil {
				t.Fatalf("Call() after re-login error = %v", err)
			}
		})
	}
}

func TestClientCapsResponseBody(t *testing.T) {
	server, armed, _ := failingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"GetMotoStatusLogResponse":{"MotoStatusLogList":"`))
		_, _ = w.Write([]byte(strings.Repeat("a", 2<<20)))
		_, _ = w.Write([]byte(`","GetMotoStatusLogResult":"OK"}}`))
	})
	client := hnap.New(hnap.Config{Host: testHost(t, server.URL), Username: "admin", Password: "secret", InsecureTLS: true})
	if err := client.EnsureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	_, err := client.Call(context.Background(), "GetMotoStatusLog", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Call() error = %v, want response size limit error", err)
	}
}

func TestClientAcceptsSchemePrefixedHost(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret"})
	host := testHost(t, server.URL)
	for _, configured := range []string{host, "https://" + host, "http://" + host, "https://" + host + "/", "HTTPS://" + host} {
		t.Run(configured, func(t *testing.T) {
			client := hnap.New(hnap.Config{Host: configured, Username: "admin", Password: "secret", InsecureTLS: true})
			if err := client.EnsureLogin(context.Background()); err != nil {
				t.Fatalf("EnsureLogin() with host %q error = %v", configured, err)
			}
		})
	}
}

func TestNormalizeHost(t *testing.T) {
	for _, test := range []struct {
		raw, want string
		wantErr   bool
	}{
		{raw: "192.168.100.1", want: "192.168.100.1"},
		{raw: " https://192.168.100.1/ ", want: "192.168.100.1"},
		{raw: "http://modem.lan:8443", want: "modem.lan:8443"},
		{raw: "HTTPS://modem.lan", want: "modem.lan"},
		{raw: "", wantErr: true},
		{raw: "https://", wantErr: true},
		{raw: "https://modem.lan/HNAP1/", wantErr: true},
		{raw: "ftp://modem.lan", wantErr: true},
		{raw: "modem.lan/path", wantErr: true},
	} {
		got, err := hnap.NormalizeHost(test.raw)
		if (err != nil) != test.wantErr {
			t.Errorf("NormalizeHost(%q) error = %v, wantErr %t", test.raw, err, test.wantErr)
			continue
		}
		if got != test.want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func leafFingerprint(server *httptest.Server) string {
	sum := sha256.Sum256(server.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

func colonUpper(fingerprint string) string {
	parts := make([]string, 0, len(fingerprint)/2)
	for i := 0; i < len(fingerprint); i += 2 {
		parts = append(parts, strings.ToUpper(fingerprint[i:i+2]))
	}
	return strings.Join(parts, ":")
}

func TestClientPinsLeafCertificate(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret"})
	good := leafFingerprint(server)
	bad := strings.Repeat("00", 32)

	tests := []struct {
		name        string
		fingerprint string
		insecure    bool
		wantErr     bool
	}{
		{name: "matching lowercase pin", fingerprint: good},
		{name: "matching colon uppercase pin", fingerprint: colonUpper(good)},
		{name: "mismatched pin", fingerprint: bad, wantErr: true},
		{name: "mismatched pin wins over insecure_tls", fingerprint: bad, insecure: true, wantErr: true},
		{name: "matching pin with insecure_tls", fingerprint: good, insecure: true},
		{name: "malformed pin fails closed", fingerprint: "not-a-fingerprint", insecure: true, wantErr: true},
		{name: "no pin and verification on", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := hnaptest.Stats(server).Requests
			client := hnap.New(hnap.Config{
				Host:                 testHost(t, server.URL),
				Username:             "admin",
				Password:             "secret",
				InsecureTLS:          test.insecure,
				TLSFingerprintSHA256: test.fingerprint,
			})
			err := client.EnsureLogin(context.Background())
			if (err != nil) != test.wantErr {
				t.Fatalf("EnsureLogin() error = %v, wantErr %t", err, test.wantErr)
			}
			if test.wantErr {
				if got := hnaptest.Stats(server).Requests - before; got != 0 {
					t.Fatalf("rejected TLS peer still received %d HTTP requests", got)
				}
			}
		})
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	want := strings.Repeat("ab", 32)
	for _, raw := range []string{want, strings.ToUpper(want), colonUpper(want), " " + colonUpper(want) + " "} {
		got, err := hnap.NormalizeFingerprint(raw)
		if err != nil || got != want {
			t.Errorf("NormalizeFingerprint(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", strings.Repeat("ab", 31), strings.Repeat("ab", 33), strings.Repeat("zz", 32), "ab:cd"} {
		if _, err := hnap.NormalizeFingerprint(raw); err == nil {
			t.Errorf("NormalizeFingerprint(%q) error = nil, want error", raw)
		}
	}
}

func TestFetchLeafFingerprintHandshakeOnly(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, cert, err := hnap.FetchLeafFingerprint(ctx, "https://"+testHost(t, server.URL), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := leafFingerprint(server); got != want {
		t.Fatalf("FetchLeafFingerprint() = %q, want %q", got, want)
	}
	if cert == nil || !cert.Equal(server.Certificate()) {
		t.Fatal("FetchLeafFingerprint() did not return the leaf certificate")
	}
	stats := hnaptest.Stats(server)
	if stats.Requests != 0 || stats.LoginAttempts != 0 {
		t.Fatalf("fingerprint fetch made HTTP requests: %+v", stats)
	}
}

func TestLogoutWithoutSessionSendsNothing(t *testing.T) {
	server := hnaptest.NewServer(t, hnaptest.ServerConfig{Password: "secret"})
	client := hnap.New(hnap.Config{Host: testHost(t, server.URL), Username: "admin", Password: "secret", InsecureTLS: true})
	if err := client.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stats := hnaptest.Stats(server); stats.Requests != 0 {
		t.Fatalf("logout without a session made %d requests", stats.Requests)
	}

	if err := client.EnsureLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stats := hnaptest.Stats(server); stats.Logouts != 1 {
		t.Fatalf("logouts after a session = %d, want 1", stats.Logouts)
	}
}
