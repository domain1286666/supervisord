package process

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/ochinchina/supervisord/signals"
)

func auxiliaryCommand(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	setDeathsig(cmd.SysProcAttr)
	cmd.Cancel = func() error { return signals.Send(cmd.Process, "KILL", true) }
	return cmd
}

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 1<<20 {
		return 0, errors.New("command output exceeds 1 MiB")
	}
	return b.Buffer.Write(data)
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err == nil && int64(len(data)) > limit {
		return nil, errors.New("input file exceeds size limit")
	}
	return data, err
}
