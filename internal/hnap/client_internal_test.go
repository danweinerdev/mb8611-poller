package hnap

import (
	"net/url"
	"testing"
)

func TestNewUsesLoginBootstrapKeyAndJSONCookie(t *testing.T) {
	client := New(Config{Host: "modem.example"}).(*client)
	if client.privateKey != "withoutloginkey" {
		t.Fatalf("initial private key = %q", client.privateKey)
	}
	client.setLoginCookie("session")
	endpoint, err := url.Parse(client.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	cookies := client.http.Jar.Cookies(endpoint)
	if len(cookies) != 1 || cookies[0].Name != "uid" || cookies[0].Value != "session" {
		t.Fatalf("cookies = %#v", cookies)
	}
}

func TestIsUnauthOnlyMatchesResultFields(t *testing.T) {
	if !isUnauth([]byte(`{"CallResponse":{"CallResult":"UN-AUTH"}}`)) {
		t.Fatal("result field was not detected")
	}
	if isUnauth([]byte(`{"message":"UN-AUTH","LogResponse":{"LogResult":"OK"}}`)) {
		t.Fatal("payload text triggered false reauthentication")
	}
}
