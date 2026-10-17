package process

import (
	"context"
	"errors"
	"time"

	"os/exec"
	"strings"
	"syscall"
	"unicode"
)

func parseCommand(command string) ([]string, error) {
	args := []string{}
	var word strings.Builder
	var quote rune
	started := false
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if ch == '\\' && quote != '\'' {
			if i+1 == len(runes) {
				return nil, errors.New("command ends with an escape")
			}
			next := runes[i+1]
			if (quote == 0 && (unicode.IsSpace(next) || next == '\'' || next == '"' || next == '\\')) || (quote == '"' && (next == '"' || next == '\\')) {
				i++
				word.WriteRune(next)
			} else {
				word.WriteRune(ch)
			}
			started = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				word.WriteRune(ch)
			}
			continue
		}
		switch {
		case ch == '\'' || ch == '"':
			quote = ch
			started = true
		case unicode.IsSpace(ch):
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(ch)
			started = true
		}
	}
	if quote != 0 {
		return nil, errors.New("command contains an unterminated quote")
	}
	if started {
		args = append(args, word.String())
	}
	if len(args) == 0 || args[0] == "" {
		return nil, errors.New("empty command")
	}
	return args, nil
}

func createCommand(command interface{}) (*exec.Cmd, error) {
	args := make([]string, 0)
	var err error

	if s, ok := command.(string); ok {
		args, err = parseCommand(s)
		if err != nil {
			return nil, err
		}
	} else if a, ok := command.([]string); ok {
		args = a
	}

	if len(args) <= 0 {
		return nil, errors.New("empty command")
	}

	cmd := exec.Command(args[0])
	if len(args) > 1 {
		cmd.Args = args
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{}

	return cmd, nil
}

func executeCommand(command interface{}) ([]byte, error) {
	parsed, err := createCommand(command)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := auxiliaryCommand(ctx, parsed.Args)
	cmd.WaitDelay = time.Second
	var output limitedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = RunCommand(cmd)
	return output.Bytes(), err
}
