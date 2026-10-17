package signals

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ToSignal resolves supported names and rejects invalid values without signaling.
func ToSignal(name string) (os.Signal, error) {
	name = strings.ToUpper(strings.TrimSpace(name))
	if !strings.HasPrefix(name, "SIG") {
		name = "SIG" + name
	}
	if signal, ok := signalMap[name]; ok {
		return signal, nil
	}
	return nil, fmt.Errorf("unsupported signal %s", name)
}

// Kill delivers signals without observing or stealing unrelated child exit statuses.
// Deprecated: use Send and wait on the exec.Cmd owned by the caller.
func Kill(process *os.Process, names []string, children bool, _ int) error {
	var result error
	for _, name := range names {
		result = errors.Join(result, Send(process, name, children))
	}
	return result
}
