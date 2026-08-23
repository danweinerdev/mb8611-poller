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
