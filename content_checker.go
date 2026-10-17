package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ochinchina/supervisord/process"
)

// ContentChecker checks whether a startup condition has been reached.
type ContentChecker interface{ Check() bool }

// BaseChecker matches bounded output without blocking writers after Check returns.
type BaseChecker struct {
	mu          sync.Mutex
	includes    []string
	found       []bool
	tail        string
	maxNeedle   int
	timeoutTime time.Time
	notify      chan struct{}
}

func NewBaseChecker(includes []string, timeout int) *BaseChecker {
	maximum := 1
	for _, value := range includes {
		maximum = max(maximum, len(value))
	}
	return &BaseChecker{includes: append([]string(nil), includes...), found: make([]bool, len(includes)), maxNeedle: min(maximum, 1<<20), timeoutTime: time.Now().Add(time.Duration(timeout) * time.Second), notify: make(chan struct{}, 1)}
}
func (b *BaseChecker) Write(data []byte) (int, error) {
	b.mu.Lock()
	combined := b.tail + string(data)
	for i, needle := range b.includes {
		if !b.found[i] && strings.Contains(combined, needle) {
			b.found[i] = true
		}
	}
	keep := max(0, b.maxNeedle-1)
	if len(combined) > keep {
		combined = combined[len(combined)-keep:]
	}
	b.tail = combined
	b.mu.Unlock()
	select {
	case b.notify <- struct{}{}:
	default:
	}
	return len(data), nil
}
func (b *BaseChecker) isReady() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, needle := range b.includes {
		if needle != "" && !b.found[i] {
			return false
		}
	}
	return true
}
func (b *BaseChecker) Check() bool {
	timer := time.NewTimer(max(0, time.Until(b.timeoutTime)))
	defer timer.Stop()
	for {
		if b.isReady() {
			return true
		}
		select {
		case <-b.notify:
		case <-timer.C:
			return false
		}
	}
}

// ScriptChecker checks a bounded external command invocation.
type ScriptChecker struct{ args []string }

func NewScriptChecker(args []string) *ScriptChecker {
	return &ScriptChecker{args: append([]string(nil), args...)}
}
func (s *ScriptChecker) Check() bool {
	if len(s.args) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.args[0], s.args[1:]...)
	cmd.WaitDelay = time.Second
	return process.RunCommand(cmd) == nil
}

// TCPChecker owns a cancellable socket and joins its reader before returning.
type TCPChecker struct {
	host        string
	port        int
	baseChecker *BaseChecker
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewTCPChecker(host string, port int, includes []string, timeout int) *TCPChecker {
	base := NewBaseChecker(includes, timeout)
	ctx, cancel := context.WithDeadline(context.Background(), base.timeoutTime)
	checker := &TCPChecker{host: host, port: port, baseChecker: base, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go checker.start()
	return checker
}
func (t *TCPChecker) start() {
	defer close(t.done)
	defer t.cancel()
	var conn net.Conn
	for {
		var err error
		conn, err = (&net.Dialer{Timeout: time.Second}).DialContext(t.ctx, "tcp", net.JoinHostPort(t.host, fmt.Sprint(t.port)))
		if err == nil {
			break
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-t.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	defer conn.Close()
	stop := context.AfterFunc(t.ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(t.baseChecker.timeoutTime)
	buffer := make([]byte, 4096)
	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			_, _ = t.baseChecker.Write(buffer[:n])
		}
		if err != nil {
			return
		}
	}
}
func (t *TCPChecker) Check() bool {
	result := t.baseChecker.Check()
	t.cancel()
	<-t.done
	return result
}

// HTTPChecker retries connection failures within one deadline.
type HTTPChecker struct {
	url         string
	timeoutTime time.Time
}

func NewHTTPChecker(url string, timeout int) *HTTPChecker {
	return &HTTPChecker{url: url, timeoutTime: time.Now().Add(time.Duration(timeout) * time.Second)}
}
func (h *HTTPChecker) Check() bool {
	ctx, cancel := context.WithDeadline(context.Background(), h.timeoutTime)
	defer cancel()
	client := &http.Client{Timeout: min(5*time.Second, max(time.Millisecond, time.Until(h.timeoutTime)))}
	for {
		req, err := http.NewRequestWithContext(ctx, "GET", h.url, nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
			_ = resp.Body.Close()
			return resp.StatusCode >= 200 && resp.StatusCode < 300
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}
