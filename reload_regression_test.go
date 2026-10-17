package main

import (
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

func TestRepeatedReloadReleasesServersAndWorkers(t *testing.T) {
	previousOutput := log.StandardLogger().Out
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	_, cfg := testSecurity(t)
	data, err := os.ReadFile(cfg.GetConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), "port=127.0.0.1:8002", "port=127.0.0.1:0", 1) + "\n[supervisord]\nlogfile=%(here)s/supervisor.log\nlogfile_timestamp_suffix=false\nlogfile_backups=0\n"
	if err = os.WriteFile(cfg.GetConfigFile(), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewSupervisor(cfg.GetConfigFile())
	defer func() {
		s.xmlRPC.Stop()
		s.procMgr.Close()
		if handle := s.supervisorLogger(); handle != nil {
			_ = handle.Close()
		}
	}()
	if err = s.Reload(RestartChanged); err != nil {
		t.Fatal(err)
	}
	initial := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		if err := s.Reload(RestartChanged); err != nil {
			t.Fatalf("reload %d: %v", i, err)
		}
	}
	s.xmlRPC.Stop()
	s.procMgr.Close()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for runtime.NumGoroutine() > initial+3 {
		select {
		case <-deadline.C:
			t.Fatalf("goroutines before=%d after=%d", initial, runtime.NumGoroutine())
		case <-ticker.C:
		}
	}
}

func TestInvalidReloadKeepsManagementAvailable(t *testing.T) {
	_, cfg := testSecurity(t)
	data, _ := os.ReadFile(cfg.GetConfigFile())
	valid := strings.Replace(string(data), "port=127.0.0.1:8002", "port=127.0.0.1:0", 1)
	_ = os.WriteFile(cfg.GetConfigFile(), []byte(valid), 0600)
	s := NewSupervisor(cfg.GetConfigFile())
	defer func() { s.xmlRPC.Stop(); s.procMgr.Close() }()
	if err := s.Reload(RestartChanged); err != nil {
		t.Fatal(err)
	}
	s.xmlRPC.mu.Lock()
	address := s.xmlRPC.listeners["tcp"].Addr().String()
	s.xmlRPC.mu.Unlock()
	_ = os.WriteFile(cfg.GetConfigFile(), []byte(valid+"\n[program:cycle]\ncommand=unused\ndepends_on=cycle\n"), 0600)
	if err := s.Reload(RestartChanged); err == nil {
		t.Fatal("invalid reload accepted")
	}
	client := &http.Client{Timeout: time.Second, Transport: trustedTransport(t, cfg.GetConfigFile())}
	defer client.CloseIdleConnections()
	response, err := client.Get("https://" + address + "/api/auth/session")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("existing management status=%d", response.StatusCode)
	}
}
