//go:build !windows

package process

import (
	"os"
	"syscall"
)

func setUserID(attr *syscall.SysProcAttr, uid, gid uint32, groups []uint32) {
	if os.Geteuid() != 0 && uint32(os.Geteuid()) == uid && uint32(os.Getegid()) == gid {
		return
	}
	// Replace inherited supplementary groups when dropping privileges.
	attr.Credential = &syscall.Credential{Uid: uid, Gid: gid, Groups: groups}
}
