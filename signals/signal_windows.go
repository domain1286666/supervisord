//go:build windows

package signals

import (
	"os"
	"syscall"
)

var signalMap = map[string]os.Signal{"SIGHUP": syscall.SIGHUP, "SIGINT": syscall.SIGINT, "SIGQUIT": syscall.SIGQUIT, "SIGKILL": syscall.SIGKILL, "SIGTERM": syscall.SIGTERM}
