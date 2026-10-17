//go:build linux

package process

import "syscall"

// ReapUnownedChild collects an adopted zombie without stealing an owned status.
func ReapUnownedChild(pid int) error {
	childOwners.Lock()
	defer childOwners.Unlock()
	if _, owned := childOwners.pids[pid]; owned {
		return nil
	}
	var status syscall.WaitStatus
	_, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	return err
}
