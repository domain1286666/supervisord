package xmlrpcclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SetTransport sets a verified TLS transport, for example with a pinned local CA.
func (r *XMLRPCClient) SetTransport(transport http.RoundTripper) { r.client.Transport = transport }

// SetSigningKey enables the local configuration-file authentication protocol.
func (r *XMLRPCClient) SetSigningKey(key []byte) { r.signingKey = append([]byte(nil), key...) }

func (r *XMLRPCClient) ensureSession() error {
	parsed, err := url.Parse(r.serverurl)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" {
		return errors.New("TCP management requires HTTPS")
	}
	if len(r.signingKey) > 0 {
		return nil
	}
	r.loginMu.Lock()
	defer r.loginMu.Unlock()
	if r.csrf != "" && time.Now().Unix() < r.expiresAt-30 {
		return nil
	}
	if r.user == "" || r.password == "" {
		return errors.New("login credentials required; use local conf or SUPERVISORD_USER/SUPERVISORD_PASSWORD")
	}
	data, _ := json.Marshal(map[string]string{"username": r.user, "password": r.password})
	req, err := http.NewRequest("POST", strings.TrimRight(r.serverurl, "/")+"/api/auth/login", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("management login failed")
	}
	var session struct {
		CSRF      string `json:"csrf_token"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&session); err != nil {
		return err
	}
	if session.CSRF == "" || session.ExpiresAt <= time.Now().Unix() {
		return errors.New("invalid management session")
	}
	r.csrf = session.CSRF
	r.expiresAt = session.ExpiresAt
	return nil
}
