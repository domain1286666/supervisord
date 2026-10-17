package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/pem"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"crypto/x509"

	"github.com/ochinchina/supervisord/internal/rpcauth"
)

func (a *webSecurity) protectRPC(next http.Handler) http.Handler {
	private, _ := x509.MarshalPKCS8PrivateKey(a.certificate.PrivateKey)
	key := rpcauth.Key(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	var mu sync.Mutex
	used := make(map[string]time.Time)
	browser := a.protect(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Supervisord ") {
			browser.ServeHTTP(w, r)
			return
		}
		timestamp, err := strconv.ParseInt(r.Header.Get("X-Supervisord-Time"), 10, 64)
		now := time.Now()
		nonce := r.Header.Get("X-Supervisord-Nonce")
		if err != nil || timestamp < now.Unix()-60 || timestamp > now.Unix()+60 || len(nonce) < 22 || len(nonce) > 64 {
			webError(w, 401, "RPC authentication failed")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
		if err != nil || len(body) > 1<<20 {
			webError(w, 413, "RPC request too large")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Supervisord ")), []byte(rpcauth.Signature(key, r, body))) != 1 {
			webError(w, 401, "RPC authentication failed")
			return
		}
		mu.Lock()
		for k, expiry := range used {
			if now.After(expiry) {
				delete(used, k)
			}
		}
		_, replay := used[nonce]
		if replay || len(used) >= 4096 {
			mu.Unlock()
			webError(w, 401, "RPC authentication failed")
			return
		}
		used[nonce] = now.Add(2 * time.Minute)
		mu.Unlock()
		next.ServeHTTP(w, r)
	})
}
