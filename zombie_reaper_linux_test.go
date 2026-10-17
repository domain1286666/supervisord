//go:build linux
// +build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ochinchina/supervisord/process"
)

// becomeSubreaper makes orphaned descendants of the test process reparent to
// it, as they would to supervisord running as PID 1.
func becomeSubreaper(t *testing.T) {
	t.Helper()
	const prSetChildSubreaper = 36
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0); errno != 0 {
		t.Skipf("prctl(PR_SET_CHILD_SUBREAPER): %v", errno)
	}
}

func startReaper(t *testing.T, grace time.Duration) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		reapOrphans(stop, grace)
		close(done)
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
}

// The reaper must never take the exit status of a child that supervisord
// waits for itself; losing it made programs with autorestart=unexpected stay
// stopped when supervisord ran as PID 1.
func TestReaperKeepsExitStatusOfWaitedChildren(t *testing.T) {
	becomeSubreaper(t)
	startReaper(t, 200*time.Millisecond)

	// go-reaper lost the status in about a third of the runs of a child that
	// lived for a few tens of milliseconds, and in a few percent otherwise
	for _, script := range []string{"exit 3", "/bin/true; exit 3", "sleep 0.05; exit 3"} {
		for i := 0; i < 60; i++ {
			cmd := exec.Command("/bin/sh", "-c", script)
			err := process.RunCommand(cmd)
			if cmd.ProcessState == nil {
				t.Fatalf("%q run %d: no exit status, wait error: %v", script, i, err)
			}
			if code := cmd.ProcessState.ExitCode(); code != 3 {
				t.Fatalf("%q run %d: exit code %d, want 3", script, i, code)
			}
		}
	}
}

// Orphans reparented to supervisord are reaped once they have been zombies
// for the grace period.
func TestReaperReapsOrphans(t *testing.T) {
	becomeSubreaper(t)
	startReaper(t, 200*time.Millisecond)

	cmd := exec.Command("/bin/sh", "-c", "sleep 0.2 >/dev/null 2>&1 & echo $!")
	var output bytes.Buffer
	cmd.Stdout = &output
	err := process.RunCommand(cmd)
	out := output.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("orphan pid %q: %v", out, err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan %d was not reaped within 3s", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReaperCannotStealDelayedOwnedWait(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 3")
	if err := process.StartCommand(cmd); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = process.WaitCommand(cmd)
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		found := false
		for _, child := range zombieChildren(os.Getpid()) {
			if child.pid == cmd.Process.Pid {
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned child did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	seen := reapOnce(os.Getpid(), 0, nil, time.Now())
	_ = reapOnce(os.Getpid(), 0, seen, time.Now().Add(time.Hour))
	_ = process.WaitCommand(cmd)
	waited = true
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 3 {
		t.Fatalf("owned exit status was stolen: %v", cmd.ProcessState)
	}
}
