package config

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"github.com/ochinchina/go-ini"
)

// loadINI preserves command quotes and backslashes; parsing argv is a later step.
// The legacy loader consumed escapes and treated quoted #/; as comments.
func loadINI(target *ini.Ini, data []byte) error {
	if len(data) > 8<<20 {
		return fmt.Errorf("configuration exceeds 8 MiB")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var section *ini.Section
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.IndexByte(line, ']')
			if end < 2 {
				return fmt.Errorf("invalid section on line %d", lineNumber)
			}
			section = target.NewSection(line[1:end])
			continue
		}
		index := strings.IndexAny(line, "=:")
		if index < 1 || section == nil {
			return fmt.Errorf("invalid configuration on line %d", lineNumber)
		}
		key, value := strings.TrimSpace(line[:index]), strings.TrimSpace(line[index+1:])
		if strings.HasPrefix(value, `"""`) {
			value = strings.TrimPrefix(value, `"""`)
			for !strings.HasSuffix(value, `"""`) {
				if !scanner.Scan() {
					return fmt.Errorf("unterminated multiline value on line %d", lineNumber)
				}
				lineNumber++
				value += "\n" + scanner.Text()
			}
			value = strings.TrimSuffix(value, `"""`)
		} else {
			for trailingContinuation(value) {
				value = strings.TrimSuffix(value, "\\")
				if !scanner.Scan() {
					return fmt.Errorf("unterminated continuation on line %d", lineNumber)
				}
				lineNumber++
				value += strings.TrimSpace(scanner.Text())
			}
			value = stripINIComment(value)
		}
		section.Add(key, value)
	}
	return scanner.Err()
}

func trailingContinuation(value string) bool {
	count := 0
	for i := len(value) - 1; i >= 0 && value[i] == '\\'; i-- {
		count++
	}
	return count%2 == 1
}
func stripINIComment(value string) string {
	var quote rune
	escaped := false
	for index, ch := range value {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if (ch == '#' || ch == ';') && index > 0 && unicode.IsSpace(rune(value[index-1])) {
			return strings.TrimSpace(value[:index])
		}
	}
	return strings.TrimSpace(value)
}

func validateDependencies(entries []*Entry) error {
	nodes := make(map[string]*Entry)
	for _, entry := range entries {
		if entry.IsProgram() {
			nodes[entry.GetProgramName()] = entry
		}
	}
	visiting, finished := make(map[string]bool), make(map[string]bool)
	var visit func(string) error
	visit = func(name string) error {
		if finished[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("cyclic depends_on involving %s", name)
		}
		entry, ok := nodes[name]
		if !ok {
			return fmt.Errorf("unknown depends_on program %s", name)
		}
		visiting[name] = true
		for _, dependency := range entry.GetStringArray("depends_on", ",") {
			dependency = strings.TrimSpace(dependency)
			if dependency != "" {
				if err := visit(dependency); err != nil {
					return err
				}
			}
		}
		delete(visiting, name)
		finished[name] = true
		return nil
	}
	for name := range nodes {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}
