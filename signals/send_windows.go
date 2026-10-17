//go:build windows

package signals

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Send requests termination through taskkill, forcibly only for KILL.
func Send(process *os.Process, name string, children bool) error {
	if process == nil || process.Pid <= 1 {
		return fmt.Errorf("invalid process for signaling")
	}
	if _, err := ToSignal(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"/PID", strconv.Itoa(process.Pid)}
	if children {
		args = append(args, "/T")
	}
	if strings.EqualFold(strings.TrimPrefix(name, "SIG"), "KILL") {
		args = append(args, "/F")
	}
	return exec.CommandContext(ctx, "taskkill", args...).Run()
}
