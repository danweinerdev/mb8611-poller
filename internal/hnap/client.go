package hnap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type Client interface {
	EnsureLogin(context.Context) error
	Login(context.Context) error
	Call(context.Context, string, any) (json.RawMessage, error)
	Multi(context.Context, ...string) (map[string]json.RawMessage, error)
	Logout(context.Context) error
}

type Config struct {
	Host                 string
	Username             string
	Password             string
	Timeout              time.Duration
	InsecureTLS          bool
	TLSFingerprintSHA256 string
}

type client struct {
	cfg        Config
	http       *http.Client
	endpoint   string
	loggedIn   bool
	privateKey string
}

// maxResponseBytes caps every HNAP response body. A full poll's
// GetMultipleHNAPs response is tens of kilobytes.
const maxResponseBytes = 1 << 20

// New returns an HNAP client for cfg. When cfg.TLSFingerprintSHA256 is set,
// the modem's leaf certificate is pinned and InsecureTLS is ignored.
func New(cfg Config) Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	host, err := NormalizeHost(cfg.Host)
	if err != nil {
		// Configuration validation rejects such hosts first; keep the raw
		// value so requests fail with a transport error naming it.
		host = strings.TrimSpace(cfg.Host)
	}
	jar, _ := cookiejar.New(nil)
	return &client{
		cfg:        cfg,
		endpoint:   "https://" + host + "/HNAP1/",
		privateKey: "withoutloginkey",
		http: &http.Client{
			Timeout:   cfg.Timeout,
			Jar:       jar,
			Transport: &http.Transport{TLSClientConfig: modemTLSConfig(cfg)},
			// A redirect means the modem is sending the client to its HTML
			// login page; never follow it. callOnce reports it as ErrUnauth.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// NormalizeHost returns the bare host[:port] for a configured modem host. A
// leading http:// or https:// (any case) and one trailing slash are accepted
// and dropped: the HNAP endpoint is always HTTPS. Paths, queries, and other
// schemes are rejected.
func NormalizeHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if scheme, rest, ok := strings.Cut(host, "://"); ok {
		switch strings.ToLower(scheme) {
		case "http", "https":
			host = rest
		default:
			return "", fmt.Errorf("hnap: unsupported scheme %q in modem host %q (HNAP always uses https)", scheme, raw)
		}
	}
	host = strings.TrimSuffix(host, "/")
	if host == "" {
		return "", fmt.Errorf("hnap: modem host %q is empty", raw)
	}
	if strings.ContainsAny(host, "/?#@ \t") {
		return "", fmt.Errorf("hnap: modem host %q must be a host or host:port without a path", raw)
	}
	parsed, err := url.Parse("https://" + host)
	if err != nil || parsed.Host != host {
		return "", fmt.Errorf("hnap: invalid modem host %q", raw)
	}
	return host, nil
}

func (c *client) EnsureLogin(ctx context.Context) error {
	if c.loggedIn {
		return nil
	}
	return c.Login(ctx)
}

func (c *client) Login(ctx context.Context) error {
	c.loggedIn = false
	c.privateKey = "withoutloginkey"
	first, err := c.callOnce(ctx, "Login", loginPayload(c.cfg.Username, "", "request"))
	if err != nil {
		return err
	}
	var challenge struct {
		LoginResponse struct{ LoginResult, PublicKey, Challenge, Cookie string }
	}
	if err := json.Unmarshal(first, &challenge); err != nil {
		return fmt.Errorf("hnap: decode login challenge: %w", err)
	}
	if challenge.LoginResponse.LoginResult == "FAILED" {
		return ErrLockedOut
	}
	c.setLoginCookie(challenge.LoginResponse.Cookie)
	c.privateKey = derivePrivateKey(challenge.LoginResponse.PublicKey, c.cfg.Password, challenge.LoginResponse.Challenge)
	second, err := c.callOnce(ctx, "Login", loginPayload(c.cfg.Username, hmacMD5(c.privateKey, challenge.LoginResponse.Challenge), "login"))
	if err != nil {
		return err
	}
	var result struct{ LoginResponse struct{ LoginResult string } }
	if err := json.Unmarshal(second, &result); err != nil {
		return fmt.Errorf("hnap: decode login result: %w", err)
	}
	if result.LoginResponse.LoginResult != "OK" {
		c.loggedIn = false
		return ErrAuthFailed
	}
	c.loggedIn = true
	return nil
}

func (c *client) Call(ctx context.Context, action string, payload any) (json.RawMessage, error) {
	response, err := c.callOnce(ctx, action, payload)
	if err != nil || !isUnauth(response) {
		return response, err
	}
	c.loggedIn = false
	if err := c.EnsureLogin(ctx); err != nil {
		return nil, err
	}
	response, err = c.callOnce(ctx, action, payload)
	if err != nil {
		return nil, err
	}
	if isUnauth(response) {
		c.loggedIn = false
		return nil, ErrUnauth
	}
	return response, nil
}

func (c *client) Multi(ctx context.Context, actions ...string) (map[string]json.RawMessage, error) {
	requested := make(map[string]string, len(actions))
	for _, action := range actions {
		requested[action] = ""
	}
	response, err := c.Call(ctx, "GetMultipleHNAPs", map[string]any{"GetMultipleHNAPs": requested})
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Response map[string]json.RawMessage `json:"GetMultipleHNAPsResponse"`
	}
	if err := json.Unmarshal(response, &wrapper); err != nil {
		return nil, fmt.Errorf("hnap: decode multiple response: %w", err)
	}
	return wrapper.Response, nil
}

// Logout ends the modem session. Without an active session it sends nothing,
// so shutdown during a lockout cooldown stays silent.
func (c *client) Logout(ctx context.Context) error {
	if !c.loggedIn {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.endpoint, "/HNAP1/")+"/Logout.html", nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(req)
	if err != nil {
		slog.Debug("hnap logout request failed", "error", err)
		c.loggedIn = false
		return nil
	}
	if response != nil {
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			slog.Debug("hnap logout rejected", "status", response.Status)
		}
	}
	c.loggedIn = false
	return nil
}

// callOnce performs one HNAP request. Any failure (transport, non-2xx status,
// redirect, oversized or non-JSON body) discards the session so the next
// EnsureLogin authenticates again instead of reusing a dead session.
func (c *client) callOnce(ctx context.Context, action string, payload any) (raw json.RawMessage, err error) {
	defer func() {
		if err != nil {
			c.loggedIn = false
		}
	}()
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("SOAPACTION", makeSOAPAction(action))
	req.Header.Set("HNAP_AUTH", makeHNAPAuth(action, c.privateKey, time.Now()))
	c.clearMalformedCookie()
	response, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, fmt.Errorf("%w: %s redirected (HTTP %s) to %q", ErrUnauth, action, response.Status, response.Header.Get("Location"))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("hnap: %s: HTTP %s", action, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("hnap: read %s response: %w", action, err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("hnap: %s response exceeds %d bytes", action, maxResponseBytes)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("hnap: decode %s response: %w", action, err)
	}
	return raw, nil
}

func (c *client) clearMalformedCookie() {
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return
	}
	c.http.Jar.SetCookies(endpoint, []*http.Cookie{{Name: "", Value: "", Path: "/HNAP1", MaxAge: -1}})
}

func (c *client) setLoginCookie(value string) {
	if value == "" {
		return
	}
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return
	}
	cookie, err := http.ParseSetCookie(value)
	if err != nil {
		cookie = &http.Cookie{Name: "uid", Value: value, Path: "/"}
	}
	c.http.Jar.SetCookies(endpoint, []*http.Cookie{cookie})
}

func loginPayload(user, password, action string) map[string]any {
	return map[string]any{"Login": map[string]string{"Action": action, "Username": user, "LoginPassword": password, "Captcha": "", "PrivateLogin": "LoginPassword"}}
}
func isUnauth(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return containsUnauthResult(value)
}

func containsUnauthResult(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.HasSuffix(key, "Result") && child == "UN-AUTH" {
				return true
			}
			if containsUnauthResult(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if containsUnauthResult(child) {
				return true
			}
		}
	}
	return false
}
