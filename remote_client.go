package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ochinchina/supervisord/types"
)

type remotePeer struct {
	base           *url.URL
	user, password string
	client         *http.Client
	transport      *http.Transport
	mu             sync.Mutex
	csrf           string
	expires        int64
}

func (n *NodeLoginInfo) connection() (*remotePeer, error) {
	n.once.Do(func() {
		base, err := url.Parse(n.url)
		if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
			n.connectionErr = errors.New("remote management requires a credential-free HTTPS URL")
			return
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if n.caFile != "" {
			data, err := os.ReadFile(n.caFile)
			if err != nil {
				n.connectionErr = err
				return
			}
			if !roots.AppendCertsFromPEM(data) {
				n.connectionErr = errors.New("invalid remote CA certificate")
				return
			}
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
		transport.ResponseHeaderTimeout = 10 * time.Second
		transport.IdleConnTimeout = 30 * time.Second
		transport.MaxIdleConnsPerHost = 4
		transport.MaxConnsPerHost = 8
		jar, _ := cookiejar.New(nil)
		n.peer = &remotePeer{base: base, user: n.user, password: n.password, transport: transport, client: &http.Client{Transport: transport, Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	})
	return n.peer, n.connectionErr
}

func (p *remotePeer) login(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.csrf != "" && time.Now().Unix() < p.expires-30 {
		return nil
	}
	data, _ := json.Marshal(map[string]string{"username": p.user, "password": p.password})
	request, err := http.NewRequestWithContext(ctx, "POST", p.base.String()+"/api/auth/login", bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("remote login status %d", response.StatusCode)
	}
	var session struct {
		CSRF    string `json:"csrf_token"`
		Expires int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&session); err != nil {
		return err
	}
	if session.CSRF == "" {
		return errors.New("invalid remote session")
	}
	p.csrf = session.CSRF
	p.expires = session.Expires
	return nil
}

func (p *remotePeer) do(ctx context.Context, method, target string, data []byte, stream bool) (*http.Response, error) {
	if err := p.login(ctx); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	resolved := p.base.ResolveReference(parsed)
	if resolved.Scheme != "https" || resolved.Host != p.base.Host || resolved.User != nil {
		return nil, errors.New("remote request escaped configured node")
	}
	request, err := http.NewRequestWithContext(ctx, method, resolved.String(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if method != "GET" {
		if len(data) > 0 {
			request.Header.Set("Content-Type", "application/json")
		}
		p.mu.Lock()
		request.Header.Set("X-CSRF-Token", p.csrf)
		p.mu.Unlock()
	}
	client := p.client
	if stream {
		clone := *client
		clone.Timeout = 0
		client = &clone
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == 401 {
		p.mu.Lock()
		p.csrf = ""
		p.mu.Unlock()
	}
	return response, nil
}

func (sr *SupervisorRestful) remoteRequest(ctx context.Context, method, target, user, password string, body io.Reader) (*http.Response, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	for _, node := range sr.remoteSupervisors.GetAllNode() {
		base, _ := url.Parse(node.url)
		if base != nil && base.Host == parsed.Host && node.user == user && node.password == password {
			peer, err := node.connection()
			if err != nil {
				return nil, err
			}
			var data []byte
			if body != nil {
				data, err = io.ReadAll(io.LimitReader(body, (1<<20)+1))
				if err != nil || len(data) > 1<<20 {
					return nil, errors.New("remote request too large")
				}
			}
			response, err := peer.do(ctx, method, target, data, false)
			if err == nil {
				response.Body = &boundedRemoteBody{ReadCloser: response.Body, remaining: 8 << 20}
			}
			return response, err
		}
	}
	return nil, errors.New("remote node is not configured")
}

type boundedRemoteBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedRemoteBody) Read(data []byte) (int, error) {
	if b.remaining < 0 {
		return 0, errors.New("remote response exceeds 8 MiB")
	}
	if int64(len(data)) > b.remaining+1 {
		data = data[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(data)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return 0, errors.New("remote response exceeds 8 MiB")
	}
	return n, err
}

// readRemoteEvents owns the body and a 45-second inactivity watchdog.
func readRemoteEvents(ctx context.Context, peer *remotePeer, target string, consume func(string, []byte) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchdog := time.AfterFunc(45*time.Second, cancel)
	defer watchdog.Stop()
	response, err := peer.do(ctx, "GET", target, nil, true)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("remote SSE status %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	event := ""
	var data []byte
	for scanner.Scan() {
		line := scanner.Text()
		watchdog.Reset(45 * time.Second)
		if line == "" {
			if event == "session-expired" {
				peer.mu.Lock()
				peer.csrf = ""
				peer.mu.Unlock()
				return errors.New("remote session expired")
			}
			if event != "" {
				if err := consume(event, data); err != nil {
					return err
				}
			}
			event = ""
			data = nil
			continue
		}
		if value, ok := strings.CutPrefix(line, "event:"); ok {
			event = strings.TrimSpace(value)
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, strings.TrimSpace(value)...)
			if len(data) > 8<<20 {
				return errors.New("remote SSE message too large")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

type remoteSnapshot struct {
	Processes []types.ProcessInfo `json:"processes"`
	Node      string              `json:"node"`
	CPU       float64             `json:"cpu_percent"`
	Memory    float64             `json:"memory_percent"`
	Total     uint64              `json:"memory_total"`
	Online    bool                `json:"online"`
}

func (a *consoleAPI) followRemote(name string, node *NodeLoginInfo) {
	peer, err := node.connection()
	if err != nil {
		return
	}
	stop := context.AfterFunc(a.ctx, peer.transport.CloseIdleConnections)
	defer stop()
	defer peer.transport.CloseIdleConnections()
	delay := time.Second
	for {
		// Every stream exit transitions this node offline before a bounded retry.
		_ = readRemoteEvents(a.ctx, peer, "/api/events?local=1", func(event string, data []byte) error {
			if event != "snapshot" {
				return nil
			}
			var snapshot remoteSnapshot
			if err := json.Unmarshal(data, &snapshot); err != nil {
				return err
			}
			snapshot.Node = name
			snapshot.Online = true
			for i := range snapshot.Processes {
				snapshot.Processes[i].Node = name
			}
			a.mu.Lock()
			a.remoteState[name] = snapshot
			a.mu.Unlock()
			delay = time.Second
			return nil
		})
		a.mu.Lock()
		snapshot := a.remoteState[name]
		snapshot.Node = name
		snapshot.Online = false
		a.remoteState[name] = snapshot
		a.mu.Unlock()
		if a.ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}
