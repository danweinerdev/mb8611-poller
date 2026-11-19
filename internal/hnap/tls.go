package hnap

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// NormalizeFingerprint returns the lowercase, colon-free hex form of a
// SHA-256 certificate fingerprint. Colons, surrounding whitespace, and case
// are ignored; anything other than 64 hex digits is an error.
func NormalizeFingerprint(raw string) (string, error) {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), ":", ""))
	if len(normalized) != 2*sha256.Size {
		return "", fmt.Errorf("must be %d hex digits (colons optional), got %d", 2*sha256.Size, len(normalized))
	}
	if _, err := hex.DecodeString(normalized); err != nil {
		return "", errors.New("must contain only hex digits and optional colons")
	}
	return normalized, nil
}

// modemTLSConfig builds the modem TLS policy. A configured fingerprint pins the
// leaf certificate (the modem's certificate is self-signed, so chain
// verification cannot succeed) and takes precedence over InsecureTLS.
func modemTLSConfig(cfg Config) *tls.Config {
	if strings.TrimSpace(cfg.TLSFingerprintSHA256) == "" {
		return &tls.Config{InsecureSkipVerify: cfg.InsecureTLS} // #nosec G402 -- explicit opt-in for the modem's self-signed cert.
	}
	var want []byte
	if normalized, err := NormalizeFingerprint(cfg.TLSFingerprintSHA256); err == nil {
		want, _ = hex.DecodeString(normalized)
	}
	// Chain and hostname verification are replaced by the pin check below.
	// ClientSessionCache stays nil, so there is no session resumption and
	// VerifyPeerCertificate runs on every handshake.
	return &tls.Config{
		InsecureSkipVerify:    true, // #nosec G402 -- replaced by VerifyPeerCertificate pinning.
		VerifyPeerCertificate: pinnedLeafVerifier(want),
	}
}

// pinnedLeafVerifier fails closed: an invalid pin, a missing certificate, or a
// fingerprint mismatch all abort the handshake.
func pinnedLeafVerifier(want []byte) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(want) != sha256.Size {
			return errors.New("hnap: modem TLS fingerprint pin is invalid")
		}
		if len(rawCerts) == 0 {
			return errors.New("hnap: modem presented no TLS certificate")
		}
		sum := sha256.Sum256(rawCerts[0])
		if subtle.ConstantTimeCompare(sum[:], want) != 1 {
			return fmt.Errorf("hnap: modem certificate SHA-256 fingerprint %s does not match the pinned fingerprint", hex.EncodeToString(sum[:]))
		}
		return nil
	}
}

// FetchLeafFingerprint performs only a TLS handshake with host (default port
// 443) and returns the SHA-256 fingerprint of the presented leaf certificate.
// It sends no HTTP request and needs no credentials. The certificate is not
// verified: the result is for an operator to compare and pin.
func FetchLeafFingerprint(ctx context.Context, host string, timeout time.Duration) (string, *x509.Certificate, error) {
	normalized, err := NormalizeHost(host)
	if err != nil {
		return "", nil, err
	}
	address := normalized
	if _, _, err := net.SplitHostPort(normalized); err != nil {
		address = net.JoinHostPort(strings.TrimSuffix(strings.TrimPrefix(normalized, "["), "]"), "443")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: timeout},
		Config:    &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- read-only fingerprint discovery.
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return "", nil, fmt.Errorf("hnap: TLS handshake with %s: %w", address, err)
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", nil, fmt.Errorf("hnap: %s presented no TLS certificate", address)
	}
	leaf := state.PeerCertificates[0]
	sum := sha256.Sum256(leaf.Raw)
	return hex.EncodeToString(sum[:]), leaf, nil
}
