package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ochinchina/supervisord/config"
	"github.com/ochinchina/supervisord/internal/rpcauth"
	"github.com/ochinchina/supervisord/logger"
	"github.com/ochinchina/supervisord/xmlrpcclient"
)

func testSecurity(t *testing.T) (*webSecurity, *config.Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "supervisord.conf")
	text := "[inet_http_server]\nport=127.0.0.1:8002\nusername=test-admin\npassword=test-password-long\n[program:worker a&b]\ncommand=unused\nautostart=false\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig(path)
	if _, err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	auth, _, err := prepareWebSecurity(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	return auth, cfg
}

func loginTestClient(t *testing.T, server *httptest.Server) (*http.Client, string) {
	t.Helper()
	client := server.Client()
	jar, _ := cookiejar.New(nil)
	client.Jar = jar
	response, err := client.Post(server.URL+"/api/auth/login", "application/json", strings.NewReader(`{"username":"test-admin","password":"test-password-long"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || session.CSRF == "" {
		t.Fatalf("login status=%d csrf=%q", response.StatusCode, session.CSRF)
	}
	cookie := response.Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("insecure cookie: %+v", cookie)
	}
	return client, session.CSRF
}

func TestSecurityMigrationAndCertificatePersistence(t *testing.T) {
	auth, cfg := testSecurity(t)
	data, err := os.ReadFile(cfg.GetConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("test-password-long")) || !bytes.Contains(data, []byte("password_hash = $2")) {
		t.Fatal("plaintext password was not removed")
	}
	if _, err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	second, password, err := prepareWebSecurity(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if password != "" || sha256.Sum256(auth.certificate.Certificate[0]) != sha256.Sum256(second.certificate.Certificate[0]) {
		t.Fatal("reinitialization replaced existing credentials or certificate")
	}
	renewed, password, err := prepareWebSecurityOptions(cfg, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if password != "" || !bytes.Equal(second.passwordHash, renewed.passwordHash) || sha256.Sum256(second.certificate.Certificate[0]) == sha256.Sum256(renewed.certificate.Certificate[0]) {
		t.Fatal("certificate renewal must change only TLS material")
	}
}

func TestHTTPSAuthenticationCSRFAndLogout(t *testing.T) {
	auth, _ := testSecurity(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", auth.login)
	mux.Handle("/protected", auth.protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })))
	mux.Handle("POST /api/auth/logout", auth.protect(http.HandlerFunc(auth.logout)))
	handler, err := secureHandler(mux)
	if err != nil {
		t.Fatal(err)
	}
	plain := httptest.NewRecorder()
	handler.ServeHTTP(plain, httptest.NewRequest("GET", "http://localhost/protected", nil))
	if plain.Code != 426 {
		t.Fatalf("HTTP status=%d, want 426", plain.Code)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/protected", nil)
	req.SetBasicAuth("test-admin", "test-password-long")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 || response.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("Basic auth accepted or challenged: %d", response.StatusCode)
	}
	client, csrf := loginTestClient(t, server)
	for _, test := range []struct {
		token, origin string
		want          int
	}{{"", "", 403}, {csrf, "https://evil.example", 403}, {csrf, server.URL, 200}} {
		req, _ := http.NewRequest("POST", server.URL+"/protected", nil)
		req.Header.Set("X-CSRF-Token", test.token)
		if test.origin != "" {
			req.Header.Set("Origin", test.origin)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.want {
			t.Errorf("CSRF origin=%q status=%d, want %d", test.origin, response.StatusCode, test.want)
		}
	}
	req, _ = http.NewRequest("POST", server.URL+"/api/auth/logout", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = client.Get(server.URL + "/protected")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("logout status=%d", response.StatusCode)
	}
}

func TestCorazaBlocksSQLInjectionAndXSS(t *testing.T) {
	handler, err := secureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	for _, payload := range []string{"/?id=1%20OR%201%3D1%20--%20", "/?q=%3Cscript%3Ealert%281%29%3C%2Fscript%3E"} {
		response, err := server.Client().Get(server.URL + payload)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Errorf("attack %q status=%d, want 403", payload, response.StatusCode)
		}
	}
	response, err := server.Client().Get(server.URL + "/?q=ordinary-program")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatalf("normal request status=%d", response.StatusCode)
	}
}

func TestHTTP2LogSSEDisconnectAndLegacyPage(t *testing.T) {
	auth, cfg := testSecurity(t)
	s := NewSupervisor(cfg.GetConfigFile())
	s.activeConfig = cfg
	t.Cleanup(s.procMgr.Close)
	p := s.procMgr.CreateProcess("test", cfg.GetProgram("worker a&b"))
	p.StdoutLog = logger.NewMemoryLogger(1, logger.NewNullLogEventEmitter())
	p.StderrLog = logger.NewMemoryLogger(1, logger.NewNullLogEventEmitter())
	_, _ = p.StdoutLog.Write([]byte("first line\n"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	console := newConsoleAPI(ctx, s, auth)
	defer func() { cancel(); console.Close() }()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", auth.login)
	mux.Handle("/api/", console.routes())
	mux.Handle("/", NewSupervisorWebgui(s).CreateHandler())
	handler, err := secureHandler(mux)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client, _ := loginTestClient(t, server)
	for i := 0; i < 8; i++ {
		requestCtx, requestCancel := context.WithCancel(t.Context())
		request, _ := http.NewRequestWithContext(requestCtx, "GET", server.URL+"/api/logs/events?name=worker+a%26b&stream=stdout", nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.ProtoMajor != 2 {
			t.Fatalf("protocol=%s, want HTTP/2", response.Proto)
		}
		reader := bufio.NewReader(response.Body)
		event, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if event != "event: log\n" {
			t.Fatalf("log event=%q", event)
		}
		requestCancel()
		response.Body.Close()
	}
	for _, path := range []string{"/log?program=worker+a%26b", "/logs?name=worker+a%26b"} {
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !bytes.Contains(body, []byte("id=\"app\"")) {
			t.Fatalf("log page %s status=%d", path, response.StatusCode)
		}
	}
}

func TestSignedRPCWithoutBasicAuth(t *testing.T) {
	auth, cfg := testSecurity(t)
	s := NewSupervisor(cfg.GetConfigFile())
	s.activeConfig = cfg
	t.Cleanup(s.procMgr.Close)
	handler, err := secureHandler(auth.protectRPC(s.xmlRPC.createRPCServer(s)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	if _, err = cfg.Load(); err != nil {
		t.Fatal(err)
	}
	entry, _ := cfg.GetInetHTTPServer()
	key, err := base64.StdEncoding.DecodeString(entry.GetString("tls_private_key", ""))
	if err != nil {
		t.Fatal(err)
	}
	client := xmlrpcclient.NewXMLRPCClient(server.URL, false)
	client.SetTransport(server.Client().Transport)
	client.SetSigningKey(rpcauth.Key(key))
	reply, err := client.GetVersion()
	if err != nil {
		t.Fatal(err)
	}
	if reply.Value != SupervisorVersion {
		t.Fatalf("RPC version=%q", reply.Value)
	}
}

func trustedTransport(t *testing.T, path string) *http.Transport {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var certificate []byte
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "tls_certificate = "); ok {
			certificate, err = base64.StdEncoding.DecodeString(strings.TrimSpace(value))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("test certificate missing")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return transport
}
