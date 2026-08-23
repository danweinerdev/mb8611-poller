package hnaptest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestServerLoginAndMultiple(t *testing.T) {
	server := NewServer(t, ServerConfig{Password: "secret", Responses: map[string]json.RawMessage{
		"GetMotoStatusLog": json.RawMessage(`{"MotoStatusLogList":"entry"}`),
	}})
	client := server.Client()
	login(t, client, server.URL, "secret")

	response := request(t, client, server.URL, "GetMultipleHNAPs", "secret", `{"GetMultipleHNAPs":{"GetMotoStatusLog":""}}`, false)
	if got := response["GetMultipleHNAPsResponse"].(map[string]any)["GetMotoStatusLogResponse"].(map[string]any)["MotoStatusLogList"]; got != "entry" {
		t.Fatalf("wrapped response = %v, want entry", got)
	}
	stats := Stats(server)
	if stats.LoginAttempts != 1 || stats.LoginSuccesses != 1 || stats.Calls["GetMultipleHNAPs"] != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestServerRejectsBadAuthAndWrongPath(t *testing.T) {
	server := NewServer(t, ServerConfig{Password: "secret"})
	client := server.Client()
	login(t, client, server.URL, "secret")
	response := request(t, client, server.URL, "GetMotoStatusLog", "secret", `{}`, true)
	if result := response["GetMotoStatusLogResponse"].(map[string]any)["GetMotoStatusLogResult"]; result != "UN-AUTH" {
		t.Fatalf("bad auth result = %v, want UN-AUTH", result)
	}

	r, err := client.Post(server.URL+"/HNAP/", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong path status = %d, want %d", r.StatusCode, http.StatusNotFound)
	}
}

func TestServerLockoutAndForcedUnauth(t *testing.T) {
	server := NewServer(t, ServerConfig{Password: "secret", FailedLoginsBeforeLockout: 1, ForceUnauthAfter: 1})
	client := server.Client()
	login(t, client, server.URL, "wrong")

	first := request(t, client, server.URL, "Login", "secret", `{"Login":{"Username":"admin","LoginPassword":"","PrivateLogin":"LoginPassword"}}`, false)
	if result := first["LoginResponse"].(map[string]any)["LoginResult"]; result != "FAILED" {
		t.Fatalf("lockout result = %v, want FAILED", result)
	}
	if !Stats(server).Locked {
		t.Fatal("server should be locked after the configured number of failed logins")
	}
	privateKey := hmacMD5(publicKey+"secret", challenge)
	lockedFinal := request(t, client, server.URL, "Login", "secret", `{"Login":{"Username":"admin","LoginPassword":"`+hmacMD5(privateKey, challenge)+`","PrivateLogin":"LoginPassword"}}`, false)
	if result := lockedFinal["LoginResponse"].(map[string]any)["LoginResult"]; result != "FAILED" {
		t.Fatalf("post-lockout final login result = %v, want FAILED", result)
	}

	server = NewServer(t, ServerConfig{Password: "secret", ForceUnauthAfter: 1})
	client = server.Client()
	login(t, client, server.URL, "secret")
	response := request(t, client, server.URL, "GetMotoStatusLog", "secret", `{}`, false)
	if result := response["GetMotoStatusLogResponse"].(map[string]any)["GetMotoStatusLogResult"]; result != "UN-AUTH" {
		t.Fatalf("forced unauth result = %v, want UN-AUTH", result)
	}
	rejected := request(t, client, server.URL, "GetMotoStatusLog", "secret", `{}`, false)
	if result := rejected["GetMotoStatusLogResponse"].(map[string]any)["GetMotoStatusLogResult"]; result != "UN-AUTH" {
		t.Fatalf("post-force unauth result = %v, want UN-AUTH", result)
	}
}

func login(t *testing.T, client *http.Client, url, password string) {
	t.Helper()
	request(t, client, url, "Login", password, `{"Login":{"Username":"admin","LoginPassword":"","PrivateLogin":"LoginPassword"}}`, false)
	privateKey := hmacMD5(publicKey+password, challenge)
	request(t, client, url, "Login", password, `{"Login":{"Username":"admin","LoginPassword":"`+hmacMD5(privateKey, challenge)+`","PrivateLogin":"LoginPassword"}}`, false)
}

func request(t *testing.T, client *http.Client, url, action, password, body string, reverse bool) map[string]any {
	t.Helper()
	privateKey := hmacMD5(publicKey+password, challenge)
	timestamp := "123456"
	auth := hmacMD5(privateKey, timestamp+`"http://purenetworks.com/HNAP1/`+action+`"`) + " " + timestamp
	if reverse {
		auth = timestamp + " " + hmacMD5(privateKey, timestamp+`"http://purenetworks.com/HNAP1/`+action+`"`)
	}
	req, err := http.NewRequest(http.MethodPost, url+"/HNAP1/", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("SOAPACTION", `"http://purenetworks.com/HNAP1/`+action+`"`)
	req.Header.Set("HNAP_AUTH", auth)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
