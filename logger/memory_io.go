package logger

// NewMemoryLogger retains at most 1 MiB, independent of write sizes.
func NewMemoryLogger(_ int, emitter LogEventEmitter) *MemoryLogger {
	return &MemoryLogger{logEventEmitter: emitter}
}
func (l *MemoryLogger) Write(data []byte) (int, error) {
	n := len(data)
	l.lock.Lock()
	l.total += int64(n)
	if n >= maxLogRead {
		l.data = append(l.data[:0], data[n-maxLogRead:]...)
	} else {
		l.data = append(l.data, data...)
		if len(l.data) > maxLogRead {
			l.data = l.data[len(l.data)-maxLogRead:]
		}
	}
	l.lock.Unlock()
	l.logEventEmitter.emitLogEvent(string(data))
	return n, nil
}
func (l *MemoryLogger) SetPid(_ int) {}
func (l *MemoryLogger) Close() error { return nil }
func (l *MemoryLogger) ReadLog(offset, length int64) (string, error) {
	l.lock.Lock()
	defer l.lock.Unlock()
	return sliceLog(string(l.data), offset, length)
}
func (l *MemoryLogger) ReadTailLog(offset, length int64) (string, int64, bool, error) {
	l.lock.Lock()
	defer l.lock.Unlock()
	if offset < 0 || length < 0 {
		return tailLog("", offset, length)
	}
	base := l.total - int64(len(l.data))
	overflow := offset < base || offset > l.total
	if overflow {
		offset = base
	}
	if l.total-offset > min(length, maxLogRead) {
		offset = l.total - min(length, maxLogRead)
		overflow = true
	}
	return string(l.data[offset-base:]), l.total, overflow, nil
}
func (l *MemoryLogger) ClearCurLogFile() error {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.data = nil
	l.total = 0
	l.generation++
	return nil
}
func (l *MemoryLogger) ClearAllLogFile() error { return l.ClearCurLogFile() }

// LogVersion changes when a file rotates or a buffer is explicitly cleared.
func LogVersion(l Logger) uint64 {
	switch value := l.(type) {
	case *FileLogger:
		value.locker.Lock()
		defer value.locker.Unlock()
		return value.generation
	case *MemoryLogger:
		value.lock.Lock()
		defer value.lock.Unlock()
		return value.generation
	case *CompositeLogger:
		value.lock.Lock()
		if len(value.loggers) == 0 {
			value.lock.Unlock()
			return 0
		}
		target := value.loggers[0]
		value.lock.Unlock()
		return LogVersion(target)
	case *LogCaptureLogger:
		return LogVersion(value.underlineLogger)
	default:
		return 0
	}
}
