package main

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteResponseSizeLimit(t *testing.T) {
	body := &boundedRemoteBody{ReadCloser: io.NopCloser(strings.NewReader("oversized")), remaining: 4}
	if _, err := io.ReadAll(body); err == nil {
		t.Fatal("oversized remote response was accepted")
	}
	if _, err := body.Read(make([]byte, 8)); err == nil {
		t.Fatal("reading again after the limit must return an error")
	}
}

func TestRemoteSSEReconnectAndCancellation(t *testing.T) {
	var logins, connections atomic.Int32
	secondConnected := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		logins.Add(1)
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "remote-session", Path: "/", Secure: true, HttpOnly: true})
		writeJSON(w, 200, map[string]any{"csrf_token": "remote-csrf", "expires_at": time.Now().Add(time.Hour).Unix()})
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(sessionCookie); err != nil {
			w.WriteHeader(401)
			return
		}
		if r.URL.Query().Get("local") != "1" {
			t.Error("remote subscription must request local-only snapshots")
		}
		count := connections.Add(1)
		sseHeaders(w)
		_, _ = fmt.Fprint(w, "event: snapshot\ndata: {\"node\":\"untrusted-alias\",\"processes\":[{\"name\":\"worker\",\"node\":\"wrong\"}]}\n\n")
		w.(http.Flusher).Flush()
		if count == 1 {
			return
		}
		if count == 2 {
			close(secondConnected)
		}
		<-r.Context().Done()
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	node := NewNodeLoginInfo("configured-node", server.URL, "admin", "password")
	node.caFile = ca
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	api := &consoleAPI{ctx: ctx, remoteState: make(map[string]remoteSnapshot)}
	done := make(chan struct{})
	go func() { defer close(done); api.followRemote(node.name, node) }()
	select {
	case <-secondConnected:
	case <-time.After(8 * time.Second):
		t.Fatal("remote did not reconnect")
	}
	api.mu.Lock()
	snapshot := api.remoteState[node.name]
	api.mu.Unlock()
	if snapshot.Node != node.name || len(snapshot.Processes) != 1 || snapshot.Processes[0].Node != node.name {
		t.Fatalf("remote identity=%+v", snapshot)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("remote reader did not close on cancellation")
	}
	if logins.Load() != 1 {
		t.Fatalf("login count=%d, want session reuse", logins.Load())
	}
}

func TestRemoteRejectsPlainHTTPAndUnknownCertificate(t *testing.T) {
	if _, err := NewNodeLoginInfo("plain", "http://127.0.0.1:8002", "a", "b").connection(); err == nil {
		t.Fatal("plain HTTP remote accepted")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	peer, err := NewNodeLoginInfo("unknown", server.URL, "a", "b").connection()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.transport.CloseIdleConnections()
	if _, err := peer.do(t.Context(), "GET", "/api/processes", nil, false); err == nil {
		t.Fatal("untrusted self-signed certificate accepted")
	}
}
