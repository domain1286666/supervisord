package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ochinchina/supervisord/config"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "__Host-supervisord"
const sessionLifetime = 8 * time.Hour

type webSession struct {
	csrf    string
	expires time.Time
}
type loginAttempt struct {
	count int
	until time.Time
}
type webSecurity struct {
	mu           sync.Mutex
	username     string
	passwordHash []byte
	sessions     map[[32]byte]webSession
	attempts     map[string]loginAttempt
	loginSlots   chan struct{}
	configFile   string
	certificate  tls.Certificate
}

func randomSecret(bytes int) (string, error) {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func generateCertificate() (string, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	hostname, _ := os.Hostname()
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Supervisord local management"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	if hostname != "" {
		template.DNSNames = append(template.DNSNames, hostname)
	}
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, address := range addresses {
			if network, ok := address.(*net.IPNet); ok && !network.IP.IsUnspecified() {
				template.IPAddresses = append(template.IPAddresses, network.IP)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), nil
}

// prepareWebSecurity migrates plaintext credentials and persists generated TLS material.
func prepareWebSecurity(cfg *config.Config, reset bool) (*webSecurity, string, error) {
	return prepareWebSecurityOptions(cfg, reset, false)
}

func prepareWebSecurityOptions(cfg *config.Config, reset, renewCertificate bool) (*webSecurity, string, error) {
	entry, ok := cfg.GetInetHTTPServer()
	if !ok {
		return nil, "", errors.New("inet_http_server section is required")
	}
	values := make(map[string]string)
	username := entry.GetString("username", "")
	hash := entry.GetString("password_hash", "")
	plaintext := entry.GetString("password", "")
	generated := ""
	if reset || hash == "" {
		if reset || plaintext == "" {
			var err error
			plaintext, err = randomSecret(24)
			if err != nil {
				return nil, "", err
			}
			generated = plaintext
		}
		if strings.HasPrefix(plaintext, "{SHA}") {
			return nil, "", errors.New("legacy SHA password cannot be migrated; run supervisord init --reset-password")
		}
		data, err := bcrypt.GenerateFromPassword([]byte(plaintext), 12)
		if err != nil {
			return nil, "", err
		}
		hash = string(data)
		values["password_hash"] = hash
	}
	if _, err := bcrypt.Cost([]byte(hash)); err != nil {
		return nil, "", errors.New("invalid password_hash")
	}
	if username == "" {
		suffix, err := randomSecret(6)
		if err != nil {
			return nil, "", err
		}
		username = "admin_" + suffix
		values["username"] = username
	}
	if entry.HasParameter("password") {
		values["password"] = ""
	}
	certData, keyData := entry.GetString("tls_certificate", ""), entry.GetString("tls_private_key", "")
	if renewCertificate {
		certData, keyData = "", ""
	}
	if (certData == "") != (keyData == "") {
		return nil, "", errors.New("both tls_certificate and tls_private_key are required")
	}
	if certData == "" {
		var err error
		certData, keyData, err = generateCertificate()
		if err != nil {
			return nil, "", err
		}
		values["tls_certificate"] = certData
		values["tls_private_key"] = keyData
	}
	certPEM, err := base64.StdEncoding.DecodeString(certData)
	if err != nil {
		return nil, "", err
	}
	keyPEM, err := base64.StdEncoding.DecodeString(keyData)
	if err != nil {
		return nil, "", err
	}
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, "", err
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, "", err
	}
	if time.Now().After(leaf.NotAfter) {
		return nil, "", errors.New("TLS certificate expired; replace certificate and key or run supervisord init --renew-certificate")
	}
	if time.Now().Before(leaf.NotBefore) {
		return nil, "", errors.New("TLS certificate is not yet valid")
	}
	certificate.Leaf = leaf
	if len(values) > 0 {
		if err := config.UpdateSectionFile(cfg.GetConfigFile(), "inet_http_server", values); err != nil {
			return nil, "", fmt.Errorf("persist security settings: %w", err)
		}
	}
	if err := os.Chmod(cfg.GetConfigFile(), 0600); err != nil {
		return nil, "", err
	}
	return &webSecurity{username: username, passwordHash: []byte(hash), sessions: make(map[[32]byte]webSession), attempts: make(map[string]loginAttempt), loginSlots: make(chan struct{}, 4), configFile: cfg.GetConfigFile(), certificate: certificate}, generated, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func webError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func decodeJSON(w http.ResponseWriter, r *http.Request, value any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("Content-Type must be application/json")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("only one JSON value is allowed")
	}
	return nil
}

func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		return err == nil && parsed.Scheme == "https" && parsed.Host == r.Host
	}
	return true
}

func (a *webSecurity) session(r *http.Request) (webSession, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) > 128 {
		return webSession{}, false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[key]
	if !ok {
		return webSession{}, false
	}
	if !time.Now().Before(session.expires) {
		delete(a.sessions, key)
		return webSession{}, false
	}
	return session, true
}

func (a *webSecurity) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := a.session(r)
		if !ok {
			webError(w, 401, "请先登录")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if !sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.csrf)) != 1 {
				webError(w, 403, "请求校验失败，请重新登录")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *webSecurity) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		webError(w, 403, "请求来源无效")
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	now := time.Now()
	a.mu.Lock()
	for key, value := range a.attempts {
		if now.After(value.until) {
			delete(a.attempts, key)
		}
	}
	attempt := a.attempts[host]
	if attempt.count >= 5 && now.Before(attempt.until) {
		a.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		webError(w, 429, "登录尝试过多，请稍后重试")
		return
	}
	if len(a.attempts) >= 4096 {
		a.mu.Unlock()
		webError(w, 429, "请稍后重试")
		return
	}
	attempt.count++
	if attempt.until.IsZero() {
		attempt.until = now.Add(time.Minute)
	}
	a.attempts[host] = attempt
	hash := append([]byte(nil), a.passwordHash...)
	username := a.username
	a.mu.Unlock()
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	default:
		webError(w, 429, "请稍后重试")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		webError(w, 400, "登录数据无效")
		return
	}
	passwordOK := bcrypt.CompareHashAndPassword(hash, []byte(body.Password)) == nil
	if !passwordOK || subtle.ConstantTimeCompare([]byte(body.Username), []byte(username)) != 1 {
		webError(w, 401, "账号或密码错误")
		return
	}
	token, err := randomSecret(32)
	if err != nil {
		webError(w, 500, "无法创建会话")
		return
	}
	csrf, err := randomSecret(32)
	if err != nil {
		webError(w, 500, "无法创建会话")
		return
	}
	a.mu.Lock()
	for key, value := range a.sessions {
		if now.After(value.expires) {
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 128 {
		a.mu.Unlock()
		webError(w, 429, "会话数量已达上限")
		return
	}
	// A password change while bcrypt was running invalidates this login attempt.
	if subtle.ConstantTimeCompare(hash, a.passwordHash) != 1 {
		a.mu.Unlock()
		webError(w, 401, "账号或密码错误")
		return
	}
	a.sessions[sha256.Sum256([]byte(token))] = webSession{csrf: csrf, expires: now.Add(sessionLifetime)}
	delete(a.attempts, host)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLifetime.Seconds())})
	writeJSON(w, 200, map[string]any{"username": username, "csrf_token": csrf, "expires_at": now.Add(sessionLifetime).Unix()})
}

func (a *webSecurity) current(w http.ResponseWriter, r *http.Request) {
	session, ok := a.session(r)
	if !ok {
		webError(w, 401, "请先登录")
		return
	}
	writeJSON(w, 200, map[string]any{"username": a.username, "csrf_token": session.csrf, "expires_at": session.expires.Unix()})
}
func (a *webSecurity) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		delete(a.sessions, sha256.Sum256([]byte(cookie.Value)))
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"success": true})
}

func (a *webSecurity) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		webError(w, 400, "请求数据无效")
		return
	}
	if len(body.Password) < 12 || len(body.Password) > 72 {
		webError(w, 400, "密码须为 12–72 字节")
		return
	}
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	default:
		webError(w, 429, "请稍后重试")
		return
	}
	a.mu.Lock()
	old := append([]byte(nil), a.passwordHash...)
	a.mu.Unlock()
	if bcrypt.CompareHashAndPassword(old, []byte(body.Current)) != nil {
		webError(w, 403, "当前密码错误")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), 12)
	if err != nil {
		webError(w, 500, "无法修改密码")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if subtle.ConstantTimeCompare(old, a.passwordHash) != 1 {
		webError(w, 409, "密码已变更，请重新登录")
		return
	}
	if err := config.UpdateSectionFile(a.configFile, "inet_http_server", map[string]string{"password_hash": string(hash), "password": ""}); err != nil {
		webError(w, 500, "无法保存配置")
		return
	}
	a.passwordHash = hash
	a.sessions = make(map[[32]byte]webSession)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"success": true})
}
