//go:build !windows

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func (s *Supervisor) checkRequiredResources() error {
	entry, ok := s.GetConfig().GetSupervisord()
	if !ok {
		return nil
	}
	for _, resource := range []struct {
		key  string
		name string
		id   int
	}{{"minfds", "NOFILE", unix.RLIMIT_NOFILE}, {"minprocs", "NPROC", unix.RLIMIT_NPROC}} {
		value := entry.GetInt(resource.key, 0)
		if value < 0 {
			return fmt.Errorf("%s must not be negative", resource.key)
		}
		if value > 0 {
			if err := s.checkMinLimit(resource.id, resource.name, uint64(value)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Supervisor) checkMinLimit(resource int, name string, required uint64) error {
	var limit unix.Rlimit
	if err := unix.Getrlimit(resource, &limit); err != nil {
		return fmt.Errorf("get %s limit: %w", name, err)
	}
	if required > uint64(limit.Max) {
		return fmt.Errorf("%s %d is greater than hard limit %d", name, required, limit.Max)
	}
	if uint64(limit.Cur) >= required {
		return nil
	}
	// Assign fields of the same native type (FreeBSD uses signed limits).
	limit.Cur = limit.Max
	if err := unix.Setrlimit(resource, &limit); err != nil {
		return fmt.Errorf("raise %s soft limit: %w", name, err)
	}
	return nil
}
