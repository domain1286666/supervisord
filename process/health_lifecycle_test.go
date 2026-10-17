package process

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ochinchina/supervisord/config"
)

func TestLivenessCloseCancelsInflightHTTP(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "test.conf")
	_ = os.WriteFile(path, []byte("[program:health]\ncommand=unused\nliveness_check_script="+server.URL+"\nliveness_check_initial_delay=0\n"), 0600)
	cfg := config.NewConfig(path)
	if _, err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	checker := NewLivenessChecker("health", cfg.GetProgram("health"))
	defer checker.Close()
	var callbacks atomic.Int32
	action := func(string) { callbacks.Add(1) }
	checker.DoLivenessCheck(action, action)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP check did not start")
	}
	done := make(chan struct{})
	go func() { checker.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("health check was not canceled and joined")
	}
	checker.DoLivenessCheck(action, action)
	if callbacks.Load() != 0 {
		t.Fatal("closed health check ran a callback")
	}
}

func TestTCPHealthProbeClosesSocket(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			closed <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = conn.Read(make([]byte, 1))
		closed <- err
	}()
	executor := NewScriptExecutorWithTimeout("tcp://"+listener.Addr().String(), 1)
	defer executor.Close()
	if err := executor.Execute(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("expected EOF from closed probe")
		}
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("health probe leaked its socket")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("health socket remained open")
	}
}
