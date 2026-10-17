package logger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedRotationPreservesCurrentLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process.log")
	output := NewFileLogger(path, 1, 1, false, NewNullLogEventEmitter(), NewNullLocker())
	t.Cleanup(func() { _ = output.Close() })
	if _, err := output.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".1", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path+".1", "block-removal"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("second")); err == nil {
		t.Fatal("rotation failure was swallowed")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "first" {
		t.Fatalf("failed rotation lost current output: %q, %v", data, err)
	}
}

func TestZeroBackupsAndTimestampRotation(t *testing.T) {
	for _, timestamp := range []bool{false, true} {
		t.Run(map[bool]string{false: "numbered", true: "timestamp"}[timestamp], func(t *testing.T) {
			t.Parallel()
			name := filepath.Join(t.TempDir(), "nested", "output.log")
			l := NewFileLogger(name, 8, 0, timestamp, NewNullLogEventEmitter(), NewNullLocker())
			defer l.Close()
			for _, text := range []string{"12345678", "current"} {
				if _, err := l.Write([]byte(text)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := l.ReadLog(0, 0)
			if err != nil || got != "current" {
				t.Fatalf("ReadLog=%q,%v", got, err)
			}
			files, _ := filepath.Glob(name + "*")
			if len(files) != 1 {
				t.Fatalf("zero backup files=%v", files)
			}
		})
	}
}

func TestMemoryTailUsesAbsoluteByteOffsets(t *testing.T) {
	l := NewMemoryLogger(1, NewNullLogEventEmitter())
	_, _ = l.Write([]byte("first\n"))
	data, offset, _, err := l.ReadTailLog(0, 65536)
	if err != nil || data != "first\n" {
		t.Fatalf("initial tail=%q,%v", data, err)
	}
	_, _ = l.Write([]byte("second\n"))
	data, next, _, err := l.ReadTailLog(offset, 65536)
	if err != nil || data != "second\n" || next != 13 {
		t.Fatalf("incremental tail=%q,%d,%v", data, next, err)
	}
	_, _ = l.Write([]byte(strings.Repeat("x", 2<<20)))
	l.lock.Lock()
	size := len(l.data)
	l.lock.Unlock()
	if size > 1<<20 {
		t.Fatalf("memory log grew to %d", size)
	}
	if _, _, _, err := l.ReadTailLog(-1, 5); err == nil {
		t.Fatal("negative offset accepted")
	}
}

func TestChanLoggerOwnsBytesAndNeverBlocks(t *testing.T) {
	channel := make(chan []byte, 1)
	l := NewChanLogger(channel)
	data := []byte("original")
	_, _ = l.Write(data)
	data[0] = 'X'
	if got := string(<-channel); got != "original" {
		t.Fatalf("aliased channel buffer=%q", got)
	}
	_, _ = l.Write(data)
	if _, err := l.Write(data); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	_ = l.Close()
	if _, err := l.Write(data); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close=%v", err)
	}
}

func TestFileOpenFailureReturnsError(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	l := NewFileLogger(filepath.Join(parent, "out.log"), 10, 1, false, NewNullLogEventEmitter(), NewNullLocker())
	defer l.Close()
	if _, err := l.Write([]byte("data")); err == nil {
		t.Fatal("log open failure was swallowed")
	}
}
