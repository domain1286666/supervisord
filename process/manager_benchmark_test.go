package process

import (
	"fmt"
	"testing"

	"github.com/ochinchina/supervisord/config"
)

func BenchmarkManagerSnapshot(b *testing.B) {
	manager := NewManager()
	b.Cleanup(manager.Close)
	for i := 0; i < 256; i++ {
		entry := config.NewEntry(".")
		entry.Name = fmt.Sprintf("program:worker-%03d", i)
		manager.procs[entry.GetProgramName()] = NewProcess("benchmark", entry)
	}
	b.ResetTimer()
	for b.Loop() {
		manager.ForEachProcess(func(p *Process) { _ = p.GetName() })
	}
}
