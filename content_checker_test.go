package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBaseCheckOk(t *testing.T) {
	checker := NewBaseChecker([]string{"Hello", "world"}, 1)
	_, _ = checker.Write([]byte("wor"))
	_, _ = checker.Write([]byte("ld Hello"))
	if !checker.Check() {
		t.Fatal("checker did not match output across writes")
	}
}

func TestBaseCheckFail(t *testing.T) {
	checker := NewBaseChecker([]string{"never"}, 0)
	if checker.Check() {
		t.Fatal("incomplete output was accepted")
	}
	done := make(chan struct{})
	go func() { _, _ = checker.Write([]byte("late output")); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("writing after timeout blocked")
	}
}

func TestTCPCheck(t *testing.T) {
	for _, test := range []struct {
		name, output string
		want         bool
	}{
		{"ready", "world Hello", true}, {"missing", "world", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			t.Cleanup(func() {
				_ = listener.Close()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("TCP fixture was not released")
				}
			})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_, _ = io.WriteString(conn, test.output)
				_, _ = io.Copy(io.Discard, conn)
			}()
			checker := NewTCPChecker("127.0.0.1", listener.Addr().(*net.TCPAddr).Port, []string{"Hello", "world"}, 1)
			if got := checker.Check(); got != test.want {
				t.Fatalf("ready=%v, want %v", got, test.want)
			}
		})
	}
}

func TestHTTPStatusCheck(t *testing.T) {
	for _, status := range []int{200, 204, 404, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		got := NewHTTPChecker(server.URL, 2).Check()
		server.Close()
		if want := status >= 200 && status < 300; got != want {
			t.Errorf("status=%d: ready=%v, want %v", status, got, want)
		}
	}
}

func TestHttpCheckTimeoutReturns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	start := time.Now()
	if NewHTTPChecker(server.URL, 1).Check() {
		t.Fatal("stalled HTTP endpoint was accepted")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
}

func TestHttpCheckRetriesOnConnectionRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	cleanup := make(chan func(), 1)
	t.Cleanup(func() { (<-cleanup)() })
	go func() {
		time.Sleep(300 * time.Millisecond)
		listener, err := net.Listen("tcp", address)
		if err != nil {
			cleanup <- func() {}
			return
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })}
		cleanup <- func() { _ = server.Close(); _ = listener.Close() }
		_ = server.Serve(listener)
	}()
	if !NewHTTPChecker("http://"+address, 3).Check() {
		t.Fatal("HTTP checker did not recover when the endpoint started")
	}
}
