//go:build windows
// +build windows

package signals

import (
	"os"
	"syscall"
	"testing"
)

func TestToSignal_HUP(t *testing.T) {
	sig, err := ToSignal("HUP")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig != syscall.SIGHUP {
		t.Errorf("expected SIGHUP, got %v", sig)
	}
}

func TestToSignal_INT(t *testing.T) {
	sig, err := ToSignal("INT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig != syscall.SIGINT {
		t.Errorf("expected SIGINT, got %v", sig)
	}
}

func TestToSignal_QUIT(t *testing.T) {
	sig, err := ToSignal("QUIT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig != syscall.SIGQUIT {
		t.Errorf("expected SIGQUIT, got %v", sig)
	}
}

func TestToSignal_KILL(t *testing.T) {
	sig, err := ToSignal("KILL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig != syscall.SIGKILL {
		t.Errorf("expected SIGKILL, got %v", sig)
	}
}

func TestToSignal_USR1_NotSupported(t *testing.T) {
	sig, err := ToSignal("USR1")
	if err == nil {
		t.Fatal("expected error for USR1 on windows")
	}
	if sig != nil {
		t.Errorf("expected nil signal, got %v", sig)
	}
}

func TestToSignal_USR2_NotSupported(t *testing.T) {
	sig, err := ToSignal("USR2")
	if err == nil {
		t.Fatal("expected error for USR2 on windows")
	}
	if sig != nil {
		t.Errorf("expected nil signal, got %v", sig)
	}
}

func TestToSignal_DefaultReturnsSIGTERM(t *testing.T) {
	sig, err := ToSignal("TERM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig != syscall.SIGTERM {
		t.Errorf("expected SIGTERM, got %v", sig)
	}
}

func TestToSignal_UnknownIsRejected(t *testing.T) {
	sig, err := ToSignal("UNKNOWN")
	if err == nil || sig != nil {
		t.Fatalf("unknown signal=%v,%v", sig, err)
	}
}

func TestToSignal_EmptyIsRejected(t *testing.T) {
	sig, err := ToSignal("")
	if err == nil || sig != nil {
		t.Fatalf("empty signal=%v,%v", sig, err)
	}
}

func TestKill_InvalidProcess(t *testing.T) {
	if err := Kill(&os.Process{Pid: -1}, []string{"TERM"}, false, 1); err == nil {
		t.Fatal("invalid process accepted")
	}
}

func TestKill_EmptySignals(t *testing.T) {
	proc, err := os.FindProcess(-1)
	if err != nil {
		t.Skipf("cannot create process handle: %v", err)
	}
	err = Kill(proc, []string{}, false, 1)
	if err != nil {
		t.Errorf("expected nil error with empty signals, got %v", err)
	}
}
