package xmlrpcclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionRefreshAfterExpiry(t *testing.T) {
	var logins atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username != "admin" || body.Password != "test-password" {
			t.Error("incorrect session login payload")
			w.WriteHeader(401)
			return
		}
		n := logins.Add(1)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: fmt.Sprint(n), Path: "/", Secure: true, HttpOnly: true})
		_ = json.NewEncoder(w).Encode(map[string]any{"csrf_token": fmt.Sprint(n), "expires_at": time.Now().Add(time.Hour).Unix()})
	})
	mux.HandleFunc("POST /RPC2", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != r.Header.Get("X-CSRF-Token") || r.Header.Get("Authorization") != "" {
			t.Error("RPC did not use the refreshed session and CSRF token")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?><methodResponse><params><param><value><string>3.0</string></value></param></params></methodResponse>`)
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	client := NewXMLRPCClient(server.URL, false)
	client.SetTransport(server.Client().Transport)
	client.SetUser("admin")
	client.SetPassword("test-password")
	for i := 0; i < 2; i++ {
		reply, err := client.GetVersion()
		if err != nil || reply.Value != "3.0" {
			t.Fatalf("RPC version=%q, %v", reply.Value, err)
		}
		client.loginMu.Lock()
		client.expiresAt = time.Now().Add(-time.Minute).Unix()
		client.loginMu.Unlock()
	}
	if got := logins.Load(); got != 2 {
		t.Fatalf("login count=%d, want 2", got)
	}
}
