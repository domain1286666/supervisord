package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ochinchina/supervisord/config"
	"github.com/ochinchina/supervisord/process"
)

func BenchmarkProcessInfo(b *testing.B) {
	path := filepath.Join(b.TempDir(), "benchmark.conf")
	if err := os.WriteFile(path, []byte("[program:worker]\ncommand=unused\ndirectory=apps\nstdout_logfile=logs/stdout.log\nstderr_logfile=logs/stderr.log\n"), 0600); err != nil {
		b.Fatal(err)
	}
	cfg := config.NewConfig(path)
	if _, err := cfg.Load(); err != nil {
		b.Fatal(err)
	}
	p := process.NewProcess("benchmark", cfg.GetProgram("worker"))
	b.Cleanup(p.Close)
	b.ResetTimer()
	for b.Loop() {
		_ = getProcessInfo("benchmark-node", p)
	}
}
