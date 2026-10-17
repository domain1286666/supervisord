//go:build !windows

package signals

import (
	"fmt"
	"os"
	"syscall"
)

// Send delivers a signal without waiting for an unrelated SIGCHLD.
func Send(process *os.Process, name string, children bool) error {
	if process == nil || process.Pid <= 1 {
		return fmt.Errorf("invalid process for signaling")
	}
	sig, err := ToSignal(name)
	if err != nil {
		return err
	}
	pid := process.Pid
	if children {
		pid = -pid
	}
	return syscall.Kill(pid, sig.(syscall.Signal))
}
