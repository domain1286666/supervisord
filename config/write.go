package config

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var fileWriteMu sync.Mutex

// UpdateSectionFile atomically updates managed keys while retaining other settings.
// An empty value removes the key, for example a migrated plaintext password.
func UpdateSectionFile(path, section string, values map[string]string) error {
	fileWriteMu.Lock()
	defer fileWriteMu.Unlock()
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("configuration must be a regular file")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	emit := func() {
		for _, key := range keys {
			if value := values[key]; value != "" {
				fmt.Fprintf(&out, "%s = %s\n", key, value)
			}
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(original))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	inside, found := false, false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inside {
				emit()
				inside = false
			}
			if trimmed == "["+section+"]" {
				if found {
					return fmt.Errorf("duplicate managed section %s", section)
				}
				inside = true
				found = true
			}
		}
		if inside {
			if key, _, ok := strings.Cut(trimmed, "="); ok {
				if _, managed := values[strings.TrimSpace(key)]; managed {
					continue
				}
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if inside {
		emit()
	}
	if !found {
		fmt.Fprintf(&out, "\n[%s]\n", section)
		emit()
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".supervisord-conf-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err = temporary.Chmod(0600); err == nil {
		_, err = temporary.Write(out.Bytes())
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
