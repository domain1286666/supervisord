// Package rpcauth authenticates local CLI requests using configuration-file authority.
package rpcauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// Key derives a separate RPC key from the configuration's private TLS material.
func Key(privatePEM []byte) []byte {
	sum := sha256.Sum256(append([]byte("supervisord rpc signing v1\x00"), privatePEM...))
	return sum[:]
}

// Signature binds method, target, time, nonce and exact body bytes.
func Signature(key []byte, r *http.Request, body []byte) string {
	hash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(strings.Join([]string{r.Method, r.URL.RequestURI(), r.Header.Get("X-Supervisord-Time"), r.Header.Get("X-Supervisord-Nonce"), hex.EncodeToString(hash[:])}, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}
