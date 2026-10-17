package process

import (
	"errors"
	"os/exec"
	"sync"
)

var childOwners = struct {
	sync.Mutex
	pids map[int]*exec.Cmd
}{pids: make(map[int]*exec.Cmd)}

// StartCommand starts a child and reserves its exit status for WaitCommand.
// Holding the ownership lock across Start prevents the PID 1 reaper racing spawn.
func StartCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("nil child command")
	}
	childOwners.Lock()
	defer childOwners.Unlock()
	if err := cmd.Start(); err != nil {
		return err
	}
	childOwners.pids[cmd.Process.Pid] = cmd
	return nil
}

// WaitCommand must be called once after a successful StartCommand.
func WaitCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("nil child command")
	}
	err := cmd.Wait()
	if cmd.Process != nil {
		childOwners.Lock()
		if childOwners.pids[cmd.Process.Pid] == cmd {
			delete(childOwners.pids, cmd.Process.Pid)
		}
		childOwners.Unlock()
	}
	return err
}

// RunCommand starts and waits for an owned auxiliary command.
func RunCommand(cmd *exec.Cmd) error {
	if err := StartCommand(cmd); err != nil {
		return err
	}
	return WaitCommand(cmd)
}
