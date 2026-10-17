package logger

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ochinchina/supervisord/events"
	"github.com/ochinchina/supervisord/faults"
)

type ChainLogTail struct {
	lock         sync.Mutex
	logTailFuncs []func(log []byte)
}

func NewChainLogTail() *ChainLogTail {
	return &ChainLogTail{logTailFuncs: make([]func(log []byte), 0)}
}

func (clt *ChainLogTail) AddLogTail(logTailFunc func(log []byte)) error {
	clt.lock.Lock()
	defer clt.lock.Unlock()

	clt.logTailFuncs = append(clt.logTailFuncs, logTailFunc)
	return nil
}

func (clt *ChainLogTail) RemoveLogTail(logTailFunc func(log []byte)) error {
	clt.lock.Lock()
	defer clt.lock.Unlock()

	for i, f := range clt.logTailFuncs {
		if fmt.Sprintf("%p", f) == fmt.Sprintf("%p", logTailFunc) {
			clt.logTailFuncs = append(clt.logTailFuncs[:i], clt.logTailFuncs[i+1:]...)
			return nil
		}
	}
	return errors.New("log tail function not found")
}

func (clt *ChainLogTail) EmitLogTail(log []byte) {
	clt.lock.Lock()
	funcs := make([]func(log []byte), len(clt.logTailFuncs))
	copy(funcs, clt.logTailFuncs)
	clt.lock.Unlock()

	for _, f := range funcs {
		f(log)
	}
}

// Logger the log interface to log program stdout/stderr logs to file
type Logger interface {
	io.WriteCloser
	SetPid(pid int)
	ReadLog(offset int64, length int64) (string, error)
	ReadTailLog(offset int64, length int64) (string, int64, bool, error)
	ClearCurLogFile() error
	ClearAllLogFile() error
}

// LogEventEmitter the interface to emit log events
type LogEventEmitter interface {
	emitLogEvent(data string)
}

// FileLogger log program stdout/stderr to file
type FileLogger struct {
	name                  string
	currentLogFileName    string
	maxSize               int64
	backups               int
	fileSize              int64
	fileNameWithTimestamp bool
	file                  *os.File
	logEventEmitter       LogEventEmitter
	locker                sync.Locker
	closed                bool
	generation            uint64
}

// SysLogger log program stdout/stderr to syslog
type SysLogger struct {
	NullLogger
	logWriter       io.WriteCloser
	logEventEmitter LogEventEmitter
}

// NullLogger discard the program stdout/stderr log
type NullLogger struct {
	logEventEmitter LogEventEmitter
}

// NullLocker no lock
type NullLocker struct {
}

// ChanLogger write log message by channel
type ChanLogger struct {
	channel chan []byte
	mu      sync.Mutex
	closed  bool
}

// CompositeLogger dispatch the log message to other loggers
type CompositeLogger struct {
	lock    sync.Mutex
	loggers []Logger
}

type MemoryLogger struct {
	lock            sync.Mutex
	logEventEmitter LogEventEmitter
	data            []byte
	total           int64
	generation      uint64
}

// NewFileLogger creates FileLogger object

func (l *FileLogger) SetPid(pid int) {
	// NOTHING TO DO
}

// open the file and truncate the file if trunc is true

func (l *FileLogger) getTimestampedFiles() []string {
	absPath, err := filepath.Abs(l.name)
	if err != nil {
		absPath = l.name
	}

	entries, err := os.ReadDir(filepath.Dir(absPath))
	if err != nil {
		fmt.Printf("Fail to read log directory --%s-- with error %v\n", filepath.Dir(absPath), err)
		return make([]string, 0)
	} else {
		files := make([]string, 0)
		for _, entry := range entries {

			if l.isTimestampedFileName(entry.Name()) {
				files = append(files, filepath.Join(filepath.Dir(absPath), entry.Name()))
			}
		}

		sort.Strings(files)

		return files
	}

}
func (l *FileLogger) isTimestampedFileName(fileName string) bool {

	if !l.fileNameWithTimestamp {
		return false
	}
	baseName := filepath.Base(l.name)
	if !strings.HasPrefix(fileName, baseName+".") {
		return false
	}

	timestampPart := fileName[len(baseName)+1:]
	return validTimestamp(timestampPart)
}

func (sl *SysLogger) Write(b []byte) (int, error) {
	sl.logEventEmitter.emitLogEvent(string(b))
	if sl.logWriter == nil {
		return 0, errors.New("not connect to syslog server")
	}
	return sl.logWriter.Write(b)
}

// Close (sys)logger
func (sl *SysLogger) Close() error {
	if sl.logWriter == nil {
		return errors.New("not connect to syslog server")
	}
	return sl.logWriter.Close()
}

// NewNullLogger creates NullLogger object
func NewNullLogger(logEventEmitter LogEventEmitter) *NullLogger {
	return &NullLogger{logEventEmitter: logEventEmitter}
}

// SetPid sets pid of program
func (l *NullLogger) SetPid(pid int) {
	// NOTHING TO DO
}

// Write log to NullLogger
func (l *NullLogger) Write(p []byte) (int, error) {
	l.logEventEmitter.emitLogEvent(string(p))
	return len(p), nil
}

// Close the NullLogger
func (l *NullLogger) Close() error {
	return nil
}

// ReadLog returns error for NullLogger
func (l *NullLogger) ReadLog(offset int64, length int64) (string, error) {
	return "", faults.NewFault(faults.NoFile, "NO_FILE")
}

// ReadTailLog returns error for NullLogger
func (l *NullLogger) ReadTailLog(offset int64, length int64) (string, int64, bool, error) {
	return "", 0, false, faults.NewFault(faults.NoFile, "NO_FILE")
}

// ClearCurLogFile returns error for NullLogger
func (l *NullLogger) ClearCurLogFile() error {
	return fmt.Errorf("no log")
}

// ClearAllLogFile returns error for NullLogger
func (l *NullLogger) ClearAllLogFile() error {
	return faults.NewFault(faults.NoFile, "NO_FILE")
}

// NewChanLogger creates ChanLogger object
func NewChanLogger(channel chan []byte) *ChanLogger {
	return &ChanLogger{channel: channel}
}

// SetPid sets program pid
func (l *ChanLogger) SetPid(pid int) {
	// NOTHING TO DO
}

// Write log to the channel
func (l *ChanLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, os.ErrClosed
	}
	data := append([]byte(nil), p...)
	select {
	case l.channel <- data:
	default:
	}
	return len(p), nil
}

func (l *ChanLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		close(l.channel)
	}
	return nil
}

func (l *ChanLogger) ReadLog(offset int64, length int64) (string, error) {
	return "", faults.NewFault(faults.NoFile, "NO_FILE")
}

// ReadTailLog returns error for ChanLogger
func (l *ChanLogger) ReadTailLog(offset int64, length int64) (string, int64, bool, error) {
	return "", 0, false, faults.NewFault(faults.NoFile, "NO_FILE")
}

// ClearCurLogFile returns error for ChanLogger
func (l *ChanLogger) ClearCurLogFile() error {
	return fmt.Errorf("no log")
}

// ClearAllLogFile returns error for ChanLogger
func (l *ChanLogger) ClearAllLogFile() error {
	return faults.NewFault(faults.NoFile, "NO_FILE")
}

// NewNullLocker creates new NullLocker object
func NewNullLocker() *NullLocker {
	return &NullLocker{}
}

// Lock is a stub function for NullLocker
func (l *NullLocker) Lock() {
}

// Unlock is a stub function for NullLocker
func (l *NullLocker) Unlock() {
}

// StdLogger stdout/stderr logger implementation
type StdLogger struct {
	NullLogger
	logEventEmitter LogEventEmitter
	writer          io.Writer
}

// NewStdoutLogger creates StdLogger object
func NewStdoutLogger(logEventEmitter LogEventEmitter) *StdLogger {
	return &StdLogger{logEventEmitter: logEventEmitter,
		writer: os.Stdout,
	}
}

// Write output to stdout/stderr
func (l *StdLogger) Write(p []byte) (int, error) {
	n, err := l.writer.Write(p)
	if err == nil {
		l.logEventEmitter.emitLogEvent(string(p))
	}
	return n, err
}

// NewStderrLogger creates stderr logger
func NewStderrLogger(logEventEmitter LogEventEmitter) *StdLogger {
	return &StdLogger{logEventEmitter: logEventEmitter,
		writer: os.Stderr,
	}
}

// LogCaptureLogger capture the log for further analysis
type LogCaptureLogger struct {
	underlineLogger        Logger
	procCommEventCapWriter io.WriteCloser
	procCommEventCapture   *events.ProcCommEventCapture
}

// NewLogCaptureLogger creates new LogCaptureLogger object
func NewLogCaptureLogger(underlineLogger Logger,
	captureMaxBytes int,
	stdType string,
	procName string,
	groupName string) *LogCaptureLogger {
	r, w := io.Pipe()
	eventCapture := events.NewProcCommEventCapture(r,
		captureMaxBytes,
		stdType,
		procName,
		groupName)
	return &LogCaptureLogger{underlineLogger: underlineLogger,
		procCommEventCapWriter: w,
		procCommEventCapture:   eventCapture}
}

// SetPid sets pid of program
func (l *LogCaptureLogger) SetPid(pid int) {
	l.procCommEventCapture.SetPid(pid)
}

// Write log to LogCaptureLogger
func (l *LogCaptureLogger) Write(p []byte) (int, error) {
	l.procCommEventCapWriter.Write(p)
	return l.underlineLogger.Write(p)
}

// Close LogCaptureLogger
func (l *LogCaptureLogger) Close() error {
	_ = l.procCommEventCapWriter.Close()
	l.procCommEventCapture.Wait()
	return l.underlineLogger.Close()
}

// ReadLog reads log from LogCaptureLogger
func (l *LogCaptureLogger) ReadLog(offset int64, length int64) (string, error) {
	return l.underlineLogger.ReadLog(offset, length)
}

// ReadTailLog tails log from LogCaptureLogger
func (l *LogCaptureLogger) ReadTailLog(offset int64, length int64) (string, int64, bool, error) {
	return l.underlineLogger.ReadTailLog(offset, length)
}

// ClearCurLogFile clears current log file
func (l *LogCaptureLogger) ClearCurLogFile() error {
	return l.underlineLogger.ClearCurLogFile()
}

// ClearAllLogFile clears all log files
func (l *LogCaptureLogger) ClearAllLogFile() error {
	return l.underlineLogger.ClearAllLogFile()
}

// NullLogEventEmitter will not emit log to any listener
type NullLogEventEmitter struct {
}

// NewNullLogEventEmitter creates new NullLogEventEmitter object
func NewNullLogEventEmitter() *NullLogEventEmitter {
	return &NullLogEventEmitter{}
}

// emitLogEvent emit the log
func (ne *NullLogEventEmitter) emitLogEvent(data string) {
}

// StdLogEventEmitter emit the Stdout/Stderr LogEvent
type StdLogEventEmitter struct {
	Type        string
	processName string
	groupName   string
	pidFunc     func() int
}

// NewStdoutLogEventEmitter creates new StdLogEventEmitter object
func NewStdoutLogEventEmitter(processName string, groupName string, procPidFunc func() int) *StdLogEventEmitter {
	return &StdLogEventEmitter{Type: "stdout",
		processName: processName,
		groupName:   groupName,
		pidFunc:     procPidFunc}
}

// NewStderrLogEventEmitter creates new StdLogEventEmitter object for emitting Stderr log events
func NewStderrLogEventEmitter(processName string, groupName string, procPidFunc func() int) *StdLogEventEmitter {
	return &StdLogEventEmitter{Type: "stderr",
		processName: processName,
		groupName:   groupName,
		pidFunc:     procPidFunc}
}

// emitLogEvent emits stdout/stderr log event (with data)
func (se *StdLogEventEmitter) emitLogEvent(data string) {
	if se.Type == "stdout" {
		events.EmitEvent(events.CreateProcessLogStdoutEvent(se.processName, se.groupName, se.pidFunc(), data))
	} else {
		events.EmitEvent(events.CreateProcessLogStderrEvent(se.processName, se.groupName, se.pidFunc(), data))
	}
}

// BackgroundWriteCloser write data in background
type BackgroundWriteCloser struct {
	io.WriteCloser
	writeCloser io.WriteCloser
	mu          sync.Mutex
	closed      bool
}

// NewBackgroundWriteCloser creates new BackgroundWriteCloser object
func NewBackgroundWriteCloser(writeCloser io.WriteCloser) *BackgroundWriteCloser {
	bw := &BackgroundWriteCloser{writeCloser: writeCloser}

	return bw
}

func (bw *BackgroundWriteCloser) Write(data []byte) (int, error) {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	if bw.closed {
		return 0, os.ErrClosed
	}
	return bw.writeCloser.Write(data)
}

func (bw *BackgroundWriteCloser) Close() error {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	if bw.closed {
		return nil
	}
	bw.closed = true
	return bw.writeCloser.Close()
}

func NewCompositeLogger(loggers []Logger) *CompositeLogger {
	return &CompositeLogger{loggers: loggers}
}

// AddLogger adds logger to CompositeLogger pool
func (cl *CompositeLogger) AddLogger(logger Logger) {
	cl.lock.Lock()
	defer cl.lock.Unlock()
	cl.loggers = append(cl.loggers, logger)
}

// RemoveLogger removes logger from CompositeLogger pool
func (cl *CompositeLogger) RemoveLogger(logger Logger) {
	cl.lock.Lock()
	defer cl.lock.Unlock()
	for i, t := range cl.loggers {
		if t == logger {
			cl.loggers = append(cl.loggers[:i], cl.loggers[i+1:]...)
			break
		}
	}
}

// Write dispatches log data to the loggers in CompositeLogger pool
func (cl *CompositeLogger) Write(p []byte) (int, error) {
	cl.lock.Lock()
	targets := append([]Logger(nil), cl.loggers...)
	cl.lock.Unlock()
	if len(targets) == 0 {
		return len(p), nil
	}
	n, err := targets[0].Write(p)
	for _, target := range targets[1:] {
		_, e := target.Write(p)
		if err == nil {
			err = e
		}
	}
	return n, err
}

func (cl *CompositeLogger) Close() (err error) {
	cl.lock.Lock()
	defer cl.lock.Unlock()

	for i, logger := range cl.loggers {
		if i == 0 {
			err = logger.Close()
		} else {
			logger.Close()
		}
	}
	return
}

// SetPid sets pid to all loggers in CompositeLogger pool
func (cl *CompositeLogger) SetPid(pid int) {
	cl.lock.Lock()
	defer cl.lock.Unlock()

	for _, logger := range cl.loggers {
		logger.SetPid(pid)
	}
}

// ReadLog read log data from first logger in CompositeLogger pool
func (cl *CompositeLogger) ReadLog(offset, length int64) (string, error) {
	cl.lock.Lock()
	target := cl.loggers[0]
	cl.lock.Unlock()
	return target.ReadLog(offset, length)
}

func (cl *CompositeLogger) ReadTailLog(offset, length int64) (string, int64, bool, error) {
	cl.lock.Lock()
	target := cl.loggers[0]
	cl.lock.Unlock()
	return target.ReadTailLog(offset, length)
}

func (cl *CompositeLogger) ClearCurLogFile() error {
	return cl.loggers[0].ClearCurLogFile()
}

// ClearAllLogFile clear all the files of first logger in CompositeLogger pool
func (cl *CompositeLogger) ClearAllLogFile() error {
	return cl.loggers[0].ClearAllLogFile()
}

// NewLogger creates logger for a program with parameters
func NewLogger(programName string, logFile string, locker sync.Locker, maxBytes int64, backups int, fileNameWithTimestamp bool, props map[string]string, logEventEmitter LogEventEmitter) Logger {
	files := splitLogFile(logFile)
	loggers := make([]Logger, 0)
	for i, f := range files {
		var lr Logger
		if i == 0 {
			lr = createLogger(programName, f, locker, maxBytes, backups, fileNameWithTimestamp, props, logEventEmitter)
		} else {
			lr = createLogger(programName, f, NewNullLocker(), maxBytes, backups, fileNameWithTimestamp, props, NewNullLogEventEmitter())
		}
		loggers = append(loggers, lr)
	}
	return NewCompositeLogger(loggers)
}

func splitLogFile(logFile string) []string {
	files := strings.Split(logFile, ",")
	for i, f := range files {
		files[i] = strings.TrimSpace(f)
	}
	return files
}

func createLogger(programName string, logFile string, locker sync.Locker, maxBytes int64, backups int, fileNameWithTimestamp bool, props map[string]string, logEventEmitter LogEventEmitter) Logger {
	switch logFile {
	case "/dev/stdout":
		return NewStdoutLogger(logEventEmitter)
	case "/dev/stderr":
		return NewStderrLogger(logEventEmitter)
	case "/dev/null", "NONE":
		return NewNullLogger(logEventEmitter)
	case "syslog":
		return NewSysLogger(programName, props, logEventEmitter)
	case "memory":
		return NewMemoryLogger(1000, logEventEmitter)
	case "AUTO":
		return NewMemoryLogger(1000, logEventEmitter)
	default:
		if strings.HasPrefix(logFile, "syslog") {
			fields := strings.Split(logFile, "@")
			if len(fields) != 2 {
				return NewNullLogger(logEventEmitter)
			}
			fields[0] = strings.TrimSpace(fields[0])
			fields[1] = strings.TrimSpace(fields[1])
			if len(fields) == 2 && fields[0] == "syslog" {
				return NewRemoteSysLogger(programName, fields[1], props, logEventEmitter)
			}
		}
		if len(logFile) > 0 {
			return NewFileLogger(logFile, maxBytes, backups, fileNameWithTimestamp, logEventEmitter, locker)
		}
		return NewNullLogger(logEventEmitter)

	}

}

type ReformatLog struct {
	output io.Writer
}

func NewReformatLog(output io.Writer) *ReformatLog {
	return &ReformatLog{output: output}
}

func (rl *ReformatLog) Write(p []byte) (n int, err error) {
	if len(p) == 0 || p[0] == '{' {
		return rl.output.Write(p)
	}
	message := string(p)

	timeStartPos := strings.Index(message, "time=\"")
	if timeStartPos != -1 {
		timeEndPos := strings.Index(message[timeStartPos+6:], "\"")
		if timeEndPos != -1 {
			timeEndPos += timeStartPos + 6
			message = message[:timeStartPos] + message[timeStartPos+6:timeEndPos] + message[timeEndPos+1:]
		}
	}

	levelStartPos := strings.Index(message, "level=")
	if levelStartPos != -1 {
		levelEndPos := strings.Index(message[levelStartPos+6:], " ")
		if levelEndPos != -1 {
			levelEndPos += levelStartPos + 6
			message = message[:levelStartPos] + "[" + message[levelStartPos+6:levelEndPos] + "] " + message[levelEndPos+1:]
		}
	}

	_, err = rl.output.Write([]byte(message))
	if err != nil {
		return 0, err
	}
	return len(p), nil

}
