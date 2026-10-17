package process

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type fileStamp struct {
	modified int64
	size     int64
}

// Each process owns one cancellable metadata scan, with bounded retention.
func watchProgramFiles(binary, dir, pattern string, changed func(bool)) func() {
	done := make(chan struct{})
	exited := make(chan struct{})
	snapshot := func() (map[string]fileStamp, error) {
		files := make(map[string]fileStamp)
		visited := 0
		if info, err := os.Stat(binary); binary != "" && err == nil {
			files[binary] = fileStamp{info.ModTime().UnixNano(), info.Size()}
		}
		if dir != "" {
			err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
				select {
				case <-done:
					return fs.ErrClosed
				default:
				}
				visited++
				if visited > 100000 {
					return errors.New("restart monitor exceeds 100000 entries; narrow its directory")
				}
				if err != nil {
					return nil
				}
				if entry.IsDir() {
					return nil
				}
				if match, _ := filepath.Match(pattern, entry.Name()); match {
					if info, err := entry.Info(); err == nil {
						files[path] = fileStamp{info.ModTime().UnixNano(), info.Size()}
					}
				}
				if len(files) > 10000 {
					return errors.New("restart monitor exceeds 10000 matches; narrow its pattern")
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
		return files, nil
	}
	go func() {
		defer close(exited)
		previous, err := snapshot()
		if err != nil {
			if !errors.Is(err, fs.ErrClosed) {
				log.WithError(err).Warn("file monitor disabled")
			}
			return
		}
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				current, err := snapshot()
				if err != nil {
					if !errors.Is(err, fs.ErrClosed) {
						log.WithError(err).Warn("file monitor disabled")
					}
					return
				}
				binaryChanged := current[binary] != previous[binary]
				different := len(current) != len(previous)
				for name, stamp := range current {
					if previous[name] != stamp {
						different = true
						break
					}
				}
				previous = current
				if different || binaryChanged {
					changed(binaryChanged)
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }); <-exited }
}
