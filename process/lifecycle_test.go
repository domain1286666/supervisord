package process

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ochinchina/supervisord/config"
)

func TestSupervisedFixture(t *testing.T) {
	if os.Getenv("SUPERVISORD_TEST_CHILD") != "1" {
		return
	}
	index := 0
	for i, value := range os.Args {
		if value == "--" {
			index = i + 1
			break
		}
	}
	args := os.Args[index:]
	if len(args) == 0 {
		os.Exit(2)
	}
	if args[0] == "relative" {
		data, err := os.ReadFile("resource.txt")
		if err != nil {
			os.Exit(3)
		}
		result, _ := json.Marshal(map[string]any{"resource": string(data), "args": args[1:], "environment": os.Getenv("OVERRIDE_ME")})
		if err := os.WriteFile("result.json", result, 0600); err != nil {
			os.Exit(4)
		}
		fmt.Println("stdout only")
		fmt.Fprintln(os.Stderr, "stderr only")
		os.Exit(0)
	}
	done := make(chan os.Signal, 1)
	if args[0] == "ignore" {
		signal.Ignore(syscall.SIGTERM, os.Interrupt)
	} else {
		signal.Notify(done, syscall.SIGTERM, os.Interrupt)
	}
	fmt.Println("ready")
	if args[0] == "wait" {
		<-done
		_ = os.WriteFile("graceful", []byte("yes"), 0600)
		os.Exit(0)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func fixtureProcess(t *testing.T, mode, extra string) *Process {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "resource.txt"), []byte("relative resource"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("[program:fixture]\ncommand=\"%s\" -test.run=TestSupervisedFixture -- %s \"double quoted\" 'single quoted'\ndirectory=work\nstartsecs=0\nautorestart=false\nenvironment=SUPERVISORD_TEST_CHILD=1,OVERRIDE_ME=child\nstdout_logfile=logs/stdout.log\nstderr_logfile=logs/stderr.log\nstdout_logfile_timestamp_suffix=false\nstderr_logfile_timestamp_suffix=false\nstopwaitsecs=1\n%s", executable, mode, extra)
	path := filepath.Join(dir, "supervisord.conf")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewConfig(path)
	if _, err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	p := NewProcess("test", cfg.GetProgram("fixture"))
	t.Cleanup(p.Close)
	return p
}

func waitProcess(t *testing.T, p *Process) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		p.lock.RLock()
		active := p.inStart
		p.lock.RUnlock()
		if !active {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("process did not finish within 5s")
		case <-ticker.C:
		}
	}
}

func TestRelativeDirectoryQuotesEnvironmentAndSeparatedLogs(t *testing.T) {
	t.Setenv("OVERRIDE_ME", "parent")
	p := fixtureProcess(t, "relative", "")
	p.Start(true)
	waitProcess(t, p)
	data, err := os.ReadFile(filepath.Join(p.workingDirectory(), "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Resource    string
		Args        []string
		Environment string
	}
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Resource != "relative resource" || result.Environment != "child" || !reflect.DeepEqual(result.Args, []string{"double quoted", "single quoted"}) {
		t.Fatalf("child result=%+v", result)
	}
	stdout, err := p.StdoutLog.ReadLog(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := p.StderrLog.ReadLog(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "stdout only") || strings.Contains(stdout, "stderr only") || !strings.Contains(stderr, "stderr only") {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestImmediateKillSuppressesRestart(t *testing.T) {
	p := fixtureProcess(t, "ignore", "autorestart=true\n")
	p.Start(true)
	if p.GetPid() == 0 {
		t.Fatal("child failed to start")
	}
	p.Kill(true)
	waitProcess(t, p)
	if p.GetState() != Stopped || p.GetPid() != 0 {
		t.Fatalf("state=%v pid=%d", p.GetState(), p.GetPid())
	}
}

func TestKillCancelsPreStopHookBeforeRestart(t *testing.T) {
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	p := fixtureProcess(t, "ignore", "pre_stop_hook="+server.URL+"\nhook_timeout=60\n")
	p.Start(true)
	p.Stop(false)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("pre-stop hook did not start")
	}
	p.Kill(true)
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("force kill left the old pre-stop hook running")
	}
	if p.GetState() != Stopped {
		t.Fatalf("after force kill: state=%v", p.GetState())
	}
	p.Start(true)
	if p.GetPid() == 0 {
		t.Fatal("restart did not create a new child after joining the stop worker")
	}
	p.Kill(true)
}

func TestStopCancelsPreStartHook(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	p := fixtureProcess(t, "ignore", "pre_start_hook="+server.URL+"\nhook_timeout=60\n")
	p.Start(false)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("pre-start hook did not start")
	}
	done := make(chan struct{})
	go func() { p.Stop(true); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel the blocked pre-start hook")
	}
	if p.GetPid() != 0 || p.GetState() != Stopped {
		t.Fatalf("cancelled start: pid=%d state=%v", p.GetPid(), p.GetState())
	}
}

func TestKillSupersedesPendingRestart(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	p := fixtureProcess(t, "ignore", "pre_stop_hook="+server.URL+"\nhook_timeout=60\n")
	p.Start(true)
	done := make(chan struct{})
	go func() { p.Restart(context.Background(), false); close(done) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not enter its pre-stop hook")
	}
	p.Kill(true)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("superseded restart did not finish")
	}
	p.lock.RLock()
	active := p.inStart
	p.lock.RUnlock()
	if active || p.GetState() != Stopped {
		t.Fatalf("force kill was undone by the pending restart: active=%v state=%v", active, p.GetState())
	}
}

func TestGracefulStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX TERM semantics; Windows uses taskkill")
	}
	p := fixtureProcess(t, "wait", "")
	p.Start(true)
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, _ := p.StdoutLog.ReadLog(0, 0)
		if strings.Contains(data, "ready") {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("child not ready")
		case <-ticker.C:
		}
	}
	p.Stop(true)
	if _, err := os.Stat(filepath.Join(p.workingDirectory(), "graceful")); err != nil {
		t.Fatalf("graceful marker: %v", err)
	}
}

func TestManagerCallbackMayReenter(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	entry := config.NewEntry(".")
	entry.Name = "program:fixture"
	manager.CreateProcess("test", entry)
	done := make(chan struct{})
	go func() { manager.ForEachProcess(func(p *Process) { _ = manager.Find(p.GetName()) }); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("manager callback deadlocked")
	}
}

func TestQuoteParserBoundaries(t *testing.T) {
	tests := []struct {
		command string
		want    []string
		bad     bool
	}{
		{`program a"b c"'d e' "" ''`, []string{"program", "ab cd e", "", ""}, false},
		{`program "say \"hi\"" 'literal\path'`, []string{"program", `say "hi"`, `literal\path`}, false},
		{`"C:\Program Files\app.exe" --file C:\temp\file`, []string{`C:\Program Files\app.exe`, "--file", `C:\temp\file`}, false},
		{"program \"unterminated", nil, true}, {"program " + string(rune(92)), nil, true}, {`"" x`, nil, true},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			got, err := parseCommand(test.command)
			if (err != nil) != test.bad || (!test.bad && !reflect.DeepEqual(got, test.want)) {
				t.Errorf("parseCommand(%q)=%q,%v want %q bad=%v", test.command, got, err, test.want, test.bad)
			}
		})
	}
}
