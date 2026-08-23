package hnap

import (
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- HNAP protocol requires HMAC-MD5.
	"fmt"
	"time"
)

const hnapActionURLPrefix = "http://purenetworks.com/HNAP1/"

func makeSOAPAction(action string) string {
	return `"` + hnapActionURLPrefix + action + `"`
}

func makeHNAPAuth(action, privateKey string, now time.Time) string {
	timestamp := now.UnixMilli() % 2_000_000_000_000
	digest := hmacMD5(privateKey, fmt.Sprintf("%d%s", timestamp, makeSOAPAction(action)))

	return fmt.Sprintf("%s %d", digest, timestamp)
}

func derivePrivateKey(publicKey, password, challenge string) string {
	return hmacMD5(publicKey+password, challenge)
}

func hmacMD5(key, message string) string {
	mac := hmac.New(md5.New, []byte(key)) // #nosec G401 -- HNAP protocol requires HMAC-MD5.
	_, _ = mac.Write([]byte(message))
	return fmt.Sprintf("%X", mac.Sum(nil))
}
