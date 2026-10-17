package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestServiceStopWaitsForSupervisorCleanup(t *testing.T) {
	supervisor := NewSupervisor(filepath.Join(t.TempDir(), "unused.conf"))
	t.Cleanup(supervisor.procMgr.Close)
	application := NewProgram()
	application.supervisor.Store(supervisor)
	stopped := make(chan error, 1)
	go func() { stopped <- application.Stop(nil) }()
	select {
	case <-supervisor.exit:
	case <-time.After(time.Second):
		t.Fatal("service stop did not request supervisor shutdown")
	}
	select {
	case err := <-stopped:
		t.Fatalf("service stop returned before the supervisor drained: %v", err)
	default:
	}
	go application.run()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service stop did not return after supervisor cleanup")
	}
}
