package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestINICommandPreservesQuotesBackslashesAndCommentCharacters(t *testing.T) {
	command := `"C:\Program Files\app.exe" --json '{"text":"a ; # b"}' --path 'C:\temp\data' --empty ""`
	path := filepath.Join(t.TempDir(), "test.conf")
	if err := os.WriteFile(path, []byte("[program:test]\ncommand="+command+" ; outside comment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(path)
	if _, err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.GetProgram("test").GetString("command", ""); got != command {
		t.Fatalf("command=%q, want %q", got, command)
	}
}
func TestDependencyCycleRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.conf")
	_ = os.WriteFile(path, []byte("[program:a]\ncommand=a\ndepends_on=b\n[program:b]\ncommand=b\ndepends_on=a\n"), 0600)
	if _, err := NewConfig(path).Load(); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("cycle error=%v", err)
	}
}
func TestRecursiveExpressionTerminates(t *testing.T) {
	if _, err := NewStringExpression("self", "%(self)s").Eval("%(self)s"); err == nil {
		t.Fatal("recursive expression accepted")
	}
	if _, err := NewStringExpression("n", "1").Eval("%(n)999999999d"); err == nil {
		t.Fatal("unbounded format width accepted")
	}
}
func TestAtomicConfigUpdatePreservesPrograms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.conf")
	_ = os.WriteFile(path, []byte("; comment\n[inet_http_server]\npassword=plaintext\nport=:8002\n[program:a]\ncommand=echo 'hello'\n"), 0600)
	if err := UpdateSectionFile(path, "inet_http_server", map[string]string{"password": "", "password_hash": "hash"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "plaintext") || !strings.Contains(string(data), "command=echo 'hello'") {
		t.Fatalf("updated config=%s", data)
	}
}

func TestInvalidProgramExpansionKeepsPublishedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.conf")
	valid := "[program:stable]\ncommand=stable\n"
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(path)
	if _, err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"[program:bad]\ncommand=%(missing_value)s\n",
		"[program:bad]\ncommand=bad\nnumprocs=2\n",
		"[program-default]\nnumprocs=10001\n[program:bad]\ncommand=bad\n",
		"[program:a]\ncommand=a\nprocess_name=same\n[program:b]\ncommand=b\nprocess_name=same\n",
	} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := cfg.Load(); err == nil {
			t.Fatalf("invalid configuration accepted: %s", invalid)
		}
		if cfg.GetProgram("stable") == nil {
			t.Fatal("failed reload replaced the active configuration")
		}
	}
}
