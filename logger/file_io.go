package logger

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const maxLogRead = 1 << 20

// NewFileLogger creates an append-only logger. Zero backups disables retention.
func NewFileLogger(name string, maxSize int64, backups int, timestamp bool, emitter LogEventEmitter, _ sync.Locker) *FileLogger {
	l := &FileLogger{name: name, maxSize: max(0, maxSize), backups: max(0, backups), fileNameWithTimestamp: timestamp, logEventEmitter: emitter, locker: &sync.Mutex{}}
	_ = l.openFile(false)
	return l
}

func (l *FileLogger) closeFileLocked() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *FileLogger) openFile(truncate bool) error {
	if err := l.closeFileLocked(); err != nil {
		return err
	}
	l.currentLogFileName = l.name
	if l.fileNameWithTimestamp {
		l.currentLogFileName = l.name + "." + time.Now().Format("2006-01-02T15-04-05.000000000")
		if !truncate {
			files := l.getTimestampedFiles()
			if len(files) > 0 {
				latest := files[len(files)-1]
				if info, err := os.Stat(latest); err == nil && (l.maxSize == 0 || info.Size() < l.maxSize) {
					l.currentLogFileName = latest
				}
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(l.name), 0750); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_RDWR | os.O_APPEND
	if truncate {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(l.currentLogFileName, flags, 0600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	l.file = file
	l.fileSize = info.Size()
	l.generation++
	if l.fileNameWithTimestamp {
		return l.backupFiles()
	}
	return nil
}

func (l *FileLogger) backupFiles() error {
	if l.fileNameWithTimestamp {
		files := l.getTimestampedFiles()
		for len(files) > l.backups+1 {
			if err := os.Remove(files[0]); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove log backup: %w", err)
			}
			files = files[1:]
		}
		return nil
	}
	if l.backups == 0 {
		return nil
	}
	if err := os.Remove(l.name + "." + strconv.Itoa(l.backups)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove oldest log backup: %w", err)
	}
	for i := l.backups - 1; i > 0; i-- {
		if err := os.Rename(l.name+"."+strconv.Itoa(i), l.name+"."+strconv.Itoa(i+1)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate log backup: %w", err)
		}
	}
	if err := os.Rename(l.name, l.name+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate current log: %w", err)
	}
	return nil
}

// Write persists output and rotates without recursively acquiring its own lock.
func (l *FileLogger) Write(p []byte) (int, error) {
	l.locker.Lock()
	if l.closed {
		l.locker.Unlock()
		return 0, os.ErrClosed
	}
	if l.file == nil {
		if err := l.openFile(false); err != nil {
			l.locker.Unlock()
			return 0, err
		}
	}
	if l.maxSize > 0 && l.fileSize >= l.maxSize {
		if err := l.closeFileLocked(); err != nil {
			l.locker.Unlock()
			return 0, err
		}
		if !l.fileNameWithTimestamp {
			if err := l.backupFiles(); err != nil {
				l.locker.Unlock()
				return 0, err
			}
		}
		if err := l.openFile(true); err != nil {
			l.locker.Unlock()
			return 0, err
		}
	}
	n, err := l.file.Write(p)
	l.fileSize += int64(n)
	l.locker.Unlock()
	if n > 0 {
		l.logEventEmitter.emitLogEvent(string(p[:n]))
	}
	return n, err
}

// Close releases the active descriptor and is safe to repeat.
func (l *FileLogger) Close() error {
	l.locker.Lock()
	defer l.locker.Unlock()
	l.closed = true
	return l.closeFileLocked()
}

func (l *FileLogger) ClearCurLogFile() error {
	l.locker.Lock()
	defer l.locker.Unlock()
	if l.closed {
		return os.ErrClosed
	}
	return l.openFile(true)
}

func (l *FileLogger) ClearAllLogFile() error {
	l.locker.Lock()
	defer l.locker.Unlock()
	if l.closed {
		return os.ErrClosed
	}
	if err := l.closeFileLocked(); err != nil {
		return err
	}
	if l.fileNameWithTimestamp {
		for _, name := range l.getTimestampedFiles() {
			if err := os.Remove(name); err != nil {
				return err
			}
		}
	} else {
		for i := 1; i <= l.backups; i++ {
			if err := os.Remove(l.name + "." + strconv.Itoa(i)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return l.openFile(true)
}

func (l *FileLogger) readWindow(offset, length int64, tail bool) (string, int64, bool, error) {
	l.locker.Lock()
	name := l.currentLogFileName
	if name == "" {
		name = l.name
	}
	file, err := os.Open(name)
	l.locker.Unlock()
	if err != nil {
		return "", 0, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, false, err
	}
	size := info.Size()
	overflow := false
	if tail {
		if offset < 0 || length < 0 {
			return "", 0, false, errors.New("negative log offset or length")
		}
		length = min(length, maxLogRead)
		if offset > size {
			offset = 0
			overflow = true
		}
		if size-offset > length {
			offset = size - length
			overflow = true
		}
	} else {
		if offset < 0 {
			if length != 0 {
				return "", 0, false, errors.New("length must be zero with negative offset")
			}
			offset = max(0, size+offset)
			length = size - offset
		}
		if length < 0 {
			return "", 0, false, errors.New("negative log length")
		}
		if length == 0 {
			length = size - min(offset, size)
		}
	}
	offset = min(offset, size)
	length = min(min(length, maxLogRead), size-offset)
	data := make([]byte, length)
	n, err := file.ReadAt(data, offset)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return string(data[:n]), offset + int64(n), overflow, err
}

func (l *FileLogger) ReadLog(offset, length int64) (string, error) {
	data, _, _, err := l.readWindow(offset, length, false)
	return data, err
}
func (l *FileLogger) ReadTailLog(offset, length int64) (string, int64, bool, error) {
	return l.readWindow(offset, length, true)
}

func sliceLog(data string, offset, length int64) (string, error) {
	size := int64(len(data))
	if offset < 0 {
		if length != 0 {
			return "", errors.New("invalid log range")
		}
		offset = max(0, size+offset)
	}
	if offset > size {
		return "", nil
	}
	if length < 0 {
		return "", errors.New("invalid log length")
	}
	if length == 0 {
		length = size - offset
	}
	return data[offset:min(size, offset+min(length, maxLogRead))], nil
}
func tailLog(data string, offset, length int64) (string, int64, bool, error) {
	if offset < 0 || length < 0 {
		return "", 0, false, errors.New("invalid log range")
	}
	size := int64(len(data))
	overflow := offset > size
	if overflow {
		offset = 0
	}
	length = min(length, maxLogRead)
	if size-offset > length {
		offset = size - length
		overflow = true
	}
	return data[offset:], size, overflow, nil
}

func validTimestamp(name string) bool {
	for _, format := range []string{"2006-01-02T15-04-05.000000000", "2006-01-02T15-04-05"} {
		if _, err := time.Parse(format, name); err == nil {
			return true
		}
	}
	return false
}

// StreamSnapshot limits every log response, including in-memory AUTO logs.
func StreamSnapshot(l Logger) (string, error) {
	if l == nil {
		return "", nil
	}
	return l.ReadLog(-64*1024, 0)
}
