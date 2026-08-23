package hnap

import (
	"testing"
	"time"
)

func TestMakeSOAPAction(t *testing.T) {
	t.Parallel()

	const action = "GetMotoStatusLog"
	want := `"http://purenetworks.com/HNAP1/GetMotoStatusLog"`
	if got := makeSOAPAction(action); got != want {
		t.Fatalf("makeSOAPAction(%q) = %q, want %q", action, got, want)
	}
}

func TestMakeHNAPAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		action     string
		privateKey string
		millis     int64
		want       string
	}{
		{
			name:       "standard timestamp",
			action:     "GetMultipleHNAPs",
			privateKey: "PRIVATEKEY",
			millis:     1_700_000_000_123,
			want:       "8582F3C37D2134F37F94647106DA3E0B 1700000000123",
		},
		{
			name:       "timestamp modulus",
			action:     "Login",
			privateKey: "ABCD",
			millis:     2_000_000_000_001,
			want:       "9C5DF12C38BA0063AD8E47938B89EF9D 1",
		},
		{
			name:       "key contains spaces",
			action:     "GetMotoStatusLog",
			privateKey: "key with spaces",
			millis:     946_684_800_123,
			want:       "8E4CC1517727A54FC756A8F5C55B1FE3 946684800123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.UnixMilli(tt.millis)
			if got := makeHNAPAuth(tt.action, tt.privateKey, now); got != tt.want {
				t.Errorf("makeHNAPAuth(%q, %q, %v) = %q, want %q", tt.action, tt.privateKey, now, got, tt.want)
			}
		})
	}
}

func TestDerivePrivateKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		publicKey string
		password  string
		challenge string
		want      string
	}{
		{
			name:      "simple vector",
			publicKey: "PUBLIC",
			password:  "password",
			challenge: "challenge",
			want:      "D9BF6A767E15622CDF70FC1149A9FFD1",
		},
		{
			name:      "mixed case vector",
			publicKey: "PUB",
			password:  "secret",
			challenge: "CHALLENGE-42",
			want:      "EA88D17A76AE66413CCB01F1CFB5BF37",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := derivePrivateKey(tt.publicKey, tt.password, tt.challenge); got != tt.want {
				t.Errorf("derivePrivateKey(%q, %q, %q) = %q, want %q", tt.publicKey, tt.password, tt.challenge, got, tt.want)
			}
		})
	}
}
