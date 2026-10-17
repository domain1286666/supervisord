package process

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ochinchina/supervisord/config"
	"github.com/ochinchina/supervisord/events"
	"github.com/ochinchina/supervisord/logger"
	"github.com/ochinchina/supervisord/signals"
	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
)

// State the state of process
type State int32

const (
	// Stopped the stopped state
	Stopped State = iota

	// Starting the starting state
	Starting = 10

	// Running the running state
	Running = 20

	// Backoff the backoff state
	Backoff = 30

	// Stopping the stopping state
	Stopping = 40

	// Exited the Exited state
	Exited = 100

	// Fatal the Fatal state
	Fatal = 200

	// Unknown the unknown state
	Unknown = 1000
)

type AtomicState struct {
	v atomic.Int32
}

func (s *AtomicState) Load() State {
	return State(s.v.Load())
}

func (s *AtomicState) Store(state State) {
	s.v.Store(int32(state))
}

func (s *AtomicState) CompareAndSwap(old, new State) bool {
	return s.v.CompareAndSwap(int32(old), int32(new))
}

// Swap stores state and returns the previous state.
func (s *AtomicState) Swap(state State) State {
	return State(s.v.Swap(int32(state)))
}

func NewAtomicState(state State) *AtomicState {
	atomicState := AtomicState{v: atomic.Int32{}}
	atomicState.Store(state)
	return &atomicState
}

var scheduler *cron.Cron = nil

func init() {
	scheduler = cron.New(cron.WithSeconds())
	scheduler.Start()
}

// String convert State to human-readable string
func (p State) String() string {
	switch p {
	case Stopped:
		return "Stopped"
	case Starting:
		return "Starting"
	case Running:
		return "Running"
	case Backoff:
		return "Backoff"
	case Stopping:
		return "Stopping"
	case Exited:
		return "Exited"
	case Fatal:
		return "Fatal"
	default:
		return "Unknown"
	}
}

type LivenessChecker struct {
	lock             sync.Mutex
	programName      string
	inChecking       atomic.Bool
	scriptExecutor   *ScriptExecutor
	initialDelay     uint32
	checkPeriod      uint32
	successThreshold uint32
	failureThreshold uint32
	successes        uint32
	failures         uint32
	successAction    string
	failureAction    string
	nextCheckTime    time.Time
	closed           bool
	wg               sync.WaitGroup
}

var livenessSlots = make(chan struct{}, 16)

func NewLivenessChecker(programName string, config *config.Entry) *LivenessChecker {
	livenessCheckScript := config.GetString("liveness_check_script", "")
	if livenessCheckScript == "" {
		return nil
	}

	checkPeriod := uint32(min(86400, max(1, config.GetInt("liveness_check_period", 60))))
	checkTimeout := uint32(min(3600, max(1, config.GetInt("liveness_check_timeout", 30))))
	initialDelay := uint32(min(86400, max(0, config.GetInt("liveness_check_initial_delay", 60))))

	return &LivenessChecker{
		programName:      programName,
		inChecking:       atomic.Bool{},
		scriptExecutor:   NewScriptExecutorWithTimeout(livenessCheckScript, checkTimeout),
		initialDelay:     initialDelay,
		checkPeriod:      checkPeriod,
		successThreshold: uint32(min(1000, max(1, config.GetInt("liveness_check_success_threshold", 1)))),
		failureThreshold: uint32(min(1000, max(1, config.GetInt("liveness_check_failure_threshold", 3)))),
		successAction:    config.GetString("liveness_check_success_action", ""),
		failureAction:    config.GetString("liveness_check_failure_action", "restart"),
		successes:        0,
		failures:         0,
		nextCheckTime:    time.Now().Add(time.Duration(initialDelay) * time.Second),
	}
}

func (l *LivenessChecker) AddSuccess() {
	l.lock.Lock()
	defer l.lock.Unlock()
	if l.successes < l.successThreshold {
		l.successes++
	}
	l.failures = 0
}

func (l *LivenessChecker) AddFailure() {
	l.lock.Lock()
	defer l.lock.Unlock()
	if l.failures < l.failureThreshold {
		l.failures++
	}
	l.successes = 0
}

func (l *LivenessChecker) IsFailed() bool {
	l.lock.Lock()
	defer l.lock.Unlock()
	return l.failures >= l.failureThreshold
}

func (l *LivenessChecker) IsSuccess() bool {
	l.lock.Lock()
	defer l.lock.Unlock()
	return l.successes >= l.successThreshold
}

func (l *LivenessChecker) UpdateCheckTime() {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.nextCheckTime = time.Now().Add(time.Duration(l.checkPeriod) * time.Second)
}

func (l *LivenessChecker) IsCheckTime() bool {
	l.lock.Lock()
	defer l.lock.Unlock()
	return time.Now().After(l.nextCheckTime)
}

func (l *LivenessChecker) DoLivenessCheck(successActionCb, failureActionCb func(string)) {
	l.lock.Lock()
	if l.closed || l.scriptExecutor == nil || time.Now().Before(l.nextCheckTime) || !l.inChecking.CompareAndSwap(false, true) {
		l.lock.Unlock()
		return
	}
	select {
	case livenessSlots <- struct{}{}:
	default:
		l.inChecking.Store(false)
		l.lock.Unlock()
		return
	}
	wasSuccess, wasFailure := l.successes >= l.successThreshold, l.failures >= l.failureThreshold
	l.wg.Add(1)
	l.lock.Unlock()
	go func() {
		defer func() { <-livenessSlots }()
		defer l.wg.Done()
		defer l.inChecking.Store(false)
		if err := l.scriptExecutor.Execute(); err != nil {
			l.AddFailure()
		} else {
			l.AddSuccess()
		}
		l.UpdateCheckTime()
		if l.scriptExecutor.ctx.Err() != nil {
			return
		}
		if successActionCb != nil && !wasSuccess && l.IsSuccess() {
			successActionCb(l.successAction)
		}
		if failureActionCb != nil && !wasFailure && l.IsFailed() {
			failureActionCb(l.failureAction)
			l.Reset()
		}
	}()
}

// Close stops new checks, cancels network or script work, and joins the worker.
func (l *LivenessChecker) Close() {
	l.lock.Lock()
	l.closed = true
	l.lock.Unlock()
	l.scriptExecutor.Close()
	l.wg.Wait()
}

func (l *LivenessChecker) Reset() {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.successes = 0
	l.failures = 0
	l.nextCheckTime = time.Now().Add(time.Duration(l.initialDelay) * time.Second)
}

// Process the program process management data
type Process struct {
	supervisorID string
	config       *config.Entry
	cmd          *exec.Cmd
	startTime    time.Time
	stopTime     time.Time
	state        *AtomicState
	// true if process is starting
	inStart bool
	// true if the process is stopped by user
	stopByUser      *atomic.Bool
	retryTimes      *atomic.Int32
	lock            sync.RWMutex
	stdin           io.WriteCloser
	StdoutLog       logger.Logger
	StderrLog       logger.Logger
	livenessChecker *LivenessChecker
	runDone         chan struct{}
	stopCh          chan struct{}
	stopSent        bool
	lastExitCode    int
	closed          atomic.Bool
	cronID          cron.EntryID
	watchCancel     func()
	workingDir      string
	stdoutPath      string
	stderrPath      string
	runGeneration   atomic.Uint64
	stdinBusy       chan struct{}
}

// NewProcess creates new Process object
func NewProcess(supervisorID string, config *config.Entry) *Process {

	proc := &Process{supervisorID: supervisorID,
		config:          config,
		cmd:             nil,
		startTime:       time.Unix(0, 0),
		stopTime:        time.Unix(0, 0),
		state:           NewAtomicState(Stopped),
		inStart:         false,
		stopByUser:      &atomic.Bool{},
		retryTimes:      &atomic.Int32{},
		livenessChecker: nil,
		stdinBusy:       make(chan struct{}, 1),
	}
	proc.config = config
	proc.cmd = nil
	proc.workingDir = proc.workingDirectory()
	proc.stdoutPath = proc.resolveLogPaths(config.GetStringExpression("stdout_logfile", "AUTO"))
	proc.stderrPath = proc.resolveLogPaths(config.GetStringExpression("stderr_logfile", "AUTO"))
	proc.livenessChecker = NewLivenessChecker(proc.GetName(), config)
	proc.addToCron()
	return proc
}

func (p *Process) GetConfig() *config.Entry {
	return p.config
}

// add this process to crontab
func (p *Process) addToCron() {
	s := p.config.GetString("cron", "")

	if s != "" {
		log.WithFields(log.Fields{"program": p.GetName()}).Info("try to create cron program with cron expression:", s)
		p.cronID, _ = scheduler.AddFunc(s, func() {
			log.WithFields(log.Fields{"program": p.GetName()}).Info("start cron program")
			if !p.IsRunning() {
				p.Start(false)
			}
		})
	}
}

func (p *Process) DoLivenessCheck() {
	if p.closed.Load() || p.livenessChecker == nil || p.GetState() != Running {
		return
	}
	generation := p.runGeneration.Load()
	current := func() bool {
		return !p.closed.Load() && !p.stopByUser.Load() && p.GetState() == Running && p.runGeneration.Load() == generation
	}
	execute := func(command string) {
		if command == "" || !current() {
			return
		}
		executor := NewScriptExecutorWithContext(p.livenessChecker.scriptExecutor.ctx, command, 60)
		defer executor.Close()
		if err := executor.Execute(); err != nil {
			log.WithError(err).WithField("program", p.GetName()).Warn("health action failed")
		}
	}
	p.livenessChecker.DoLivenessCheck(execute, func(action string) {
		if !current() {
			return
		}
		switch strings.ToLower(action) {
		case "restart":
			p.Stop(true)
			if !p.closed.Load() {
				p.Start(false)
			}
		case "stop":
			p.Stop(true)
		default:
			execute(action)
		}
	})
}

func (p *Process) GetName() string {
	if p.config.IsProgram() {
		return p.config.GetProgramName()
	} else if p.config.IsEventListener() {
		return p.config.GetEventListenerName()
	} else {
		return ""
	}
}

// GetGroup returns group the program belongs to
func (p *Process) GetGroup() string {
	return p.config.Group
}

// GetDescription returns process status description
func (p *Process) GetDescription() string {
	state := p.state.Load()

	p.lock.RLock()
	defer p.lock.RUnlock()

	if state == Running && p.cmd != nil && p.cmd.Process != nil {
		seconds := int(time.Since(p.startTime).Seconds())
		minutes := seconds / 60
		hours := minutes / 60
		days := hours / 24
		if days > 0 {
			return fmt.Sprintf("pid %d, uptime %d days, %d:%02d:%02d", p.cmd.Process.Pid, days, hours%24, minutes%60, seconds%60)
		}
		return fmt.Sprintf("pid %d, uptime %d:%02d:%02d", p.cmd.Process.Pid, hours%24, minutes%60, seconds%60)
	} else if state != Stopped {
		if p.stopTime.Unix() > 0 {
			return p.stopTime.String()
		}
	}
	return ""
}

// GetExitstatus returns exit status of the process if the program exit
func (p *Process) GetExitstatus() int { p.lock.RLock(); defer p.lock.RUnlock(); return p.lastExitCode }

func (p *Process) GetPid() int {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if !p.IsRunning() || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *Process) GetState() State {
	return p.state.Load()
}

// GetStartTime returns process start time
func (p *Process) GetStartTime() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.startTime
}

// GetStopTime returns process stop time
func (p *Process) GetStopTime() time.Time {
	p.lock.RLock()
	defer p.lock.RUnlock()
	switch p.state.Load() {
	case Starting:
		fallthrough
	case Running:
		fallthrough
	case Stopping:
		return time.Unix(0, 0)
	default:
		return p.stopTime
	}
}

// GetStdoutLogfile returns program stdout log filename
func (p *Process) GetStdoutLogfile() string {
	return p.stdoutPath
}

func (p *Process) GetStderrLogfile() string {
	return p.stderrPath
}

func (p *Process) getStartSeconds() int64 {
	return int64(p.config.GetInt("startsecs", 1))
}

func (p *Process) getRestartPause() int {
	return p.config.GetInt("restartpause", 0)
}

func (p *Process) getStartRetries() int32 {
	return int32(p.config.GetInt("startretries", 3))
}

func (p *Process) isAutoStart() bool {
	return p.config.GetBool("autostart", true)
}

// GetPriority returns program priority (as it set in config) with default value of 999
func (p *Process) GetPriority() int {
	return p.config.GetInt("priority", 999)
}

func (p *Process) executePreStartHook() {
	hook := p.config.GetString("pre_start_hook", "")
	if hook != "" {
		log.WithField("program", p.GetName()).Info("execute pre_start_hook")
		p.executeHook(hook)
	}

}

func (p *Process) executePreStopHook() {
	hook := p.config.GetString("pre_stop_hook", "")
	if hook != "" {
		log.WithField("program", p.GetName()).Info("execute pre_stop_hook")
		p.executeHook(hook)
	}
}

func (p *Process) executeHook(cmd string) {
	if cmd == "" {
		return
	}

	scriptExecutor := NewScriptExecutorWithTimeout(cmd, uint32(min(60, max(1, p.config.GetInt("hook_timeout", 10)))))
	defer scriptExecutor.Close()
	err := scriptExecutor.Execute()
	if err != nil {
		log.WithError(err).WithField("program", p.GetName()).Error("fail to execute hook")
	} else {
		log.WithField("program", p.GetName()).Info("success to execute hook")
	}
}

// SendProcessStdin sends data to process stdin
func (p *Process) SendProcessStdin(chars string) error {
	if len(chars) > 64*1024 {
		return fmt.Errorf("stdin write exceeds 64 KiB")
	}
	select {
	case p.stdinBusy <- struct{}{}:
		defer func() { <-p.stdinBusy }()
	default:
		return fmt.Errorf("stdin write is busy")
	}
	p.lock.RLock()
	stdin := p.stdin
	p.lock.RUnlock()
	if stdin == nil {
		return fmt.Errorf("stdin is not available")
	}
	done := make(chan error, 1)
	go func() { _, err := io.WriteString(stdin, chars); done <- err }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		_ = stdin.Close()
		<-done
		return fmt.Errorf("stdin write timed out; pipe closed")
	}
}

func (p *Process) isAutoRestart() bool {
	switch p.config.GetString("autorestart", "unexpected") {
	case "false":
		return false
	case "true":
		return true
	default:
		return !p.inExitCodes(p.GetExitstatus())
	}
}

func (p *Process) inExitCodes(exitCode int) bool {

	return slices.Contains(p.getExitCodes(), exitCode)
}

func (p *Process) getExitCode() (int, error) {
	if p.cmd == nil || p.cmd.ProcessState == nil {
		return -1, fmt.Errorf("no exit code")
	}
	if status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		return status.ExitStatus(), nil
	}

	return -1, fmt.Errorf("no exit code")

}

func (p *Process) getExitCodes() []int {
	strExitCodes := strings.Split(p.config.GetString("exitcodes", "0,2"), ",")
	result := make([]int, 0)
	for _, val := range strExitCodes {
		i, err := strconv.Atoi(val)
		if err == nil {
			result = append(result, i)
		}
	}
	return result
}

// check if the process is running or not
func (p *Process) IsRunning() bool {
	state := p.state.Load()
	return state == Starting || state == Running || state == Stopping

}

// create Command object for the program
func (p *Process) createProgramCommand() error {
	args, err := parseCommand(p.config.GetStringExpression("command", ""))

	if err != nil {
		return err
	}
	p.cmd, err = createCommand(args)
	if err != nil {
		return err
	}
	if p.setUser() != nil {
		log.WithFields(log.Fields{"user": p.config.GetString("user", "")}).Error("fail to run as user")
		return fmt.Errorf("fail to set user")
	}
	setDeathsig(p.cmd.SysProcAttr)
	p.setEnv()
	p.setDir()
	if strings.ContainsAny(args[0], "/\\") && !filepath.IsAbs(args[0]) {
		p.cmd.Path = filepath.Join(p.cmd.Dir, args[0])
		p.cmd.Err = nil
	}
	p.cmd.WaitDelay = 2 * time.Second
	p.setProgramRestartChangeMonitor(p.cmd.Path)
	if info, err := os.Stat(p.cmd.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("invalid program directory %s", p.cmd.Dir)
	}
	p.setLog()

	if err = p.setProgramStdin(); err != nil {
		return err
	}

	p.stdin = nil
	if p.cmd.Stdin == nil && !p.config.IsEventListener() {
		p.stdin, err = p.cmd.StdinPipe()
		if err != nil {
			return err
		}
	}
	return nil

}

// setProgramStdin loads the configured stdin content before the program starts.
func (p *Process) setProgramStdin() error {
	var stdinType string
	stdin := p.config.GetStringExpression("stdin", "")
	for _, prefix := range []string{"string://", "file://", "command://"} {
		if strings.HasPrefix(strings.ToLower(stdin), prefix) {
			stdinType = strings.TrimSuffix(prefix, "://")
			stdin = stdin[len(prefix):]
			break
		}
	}
	if stdin == "" && !p.config.HasParameter("stdin") {
		return nil
	}

	var (
		content []byte
		err     error
	)
	switch stdinType {
	case "", "string":
		content = []byte(stdin)
	case "file":
		if !filepath.IsAbs(stdin) {
			stdin = filepath.Join(p.workingDirectory(), stdin)
		}
		content, err = readLimitedFile(stdin, 16<<20)
	case "command":
		content, err = executeCommand(stdin)
	default:
		return fmt.Errorf("unsupported stdin type %q", stdinType)
	}
	if err != nil {
		return fmt.Errorf("failed to load stdin: %w", err)
	}
	p.cmd.Stdin = bytes.NewReader(content)
	return nil
}

func (p *Process) setProgramRestartChangeMonitor(programPath string) {
	if p.watchCancel != nil {
		return
	}
	binary := ""
	if p.config.GetBool("restart_when_binary_changed", false) {
		binary = programPath
	}
	dir := p.config.GetStringExpression("restart_directory_monitor", "")
	if dir != "" && !filepath.IsAbs(dir) {
		dir = filepath.Join(p.workingDirectory(), dir)
	}
	if binary == "" && dir == "" {
		return
	}
	p.watchCancel = watchProgramFiles(binary, dir, p.config.GetString("restart_file_pattern", "*"), func(binaryChanged bool) {
		if p.closed.Load() {
			return
		}
		prefix := "file"
		if binaryChanged {
			prefix = "binary"
		}
		command := p.config.GetString("restart_cmd_when_"+prefix+"_changed", "")
		signal := p.config.GetString("restart_signal_when_"+prefix+"_changed", "")
		if command != "" {
			executor := NewScriptExecutorWithTimeout(command, 10)
			defer executor.Close()
			if err := executor.Execute(); err != nil {
				log.WithError(err).WithField("program", p.GetName()).Warn("file change hook failed")
			}
			return
		}
		if signal != "" {
			_ = p.Signal(signal, true)
			return
		}
		p.Stop(true)
		if !p.closed.Load() {
			p.Start(false)
		}
	})
}

func (p *Process) changeStateTo(procState State) {

	state := p.state.Load()
	if state == procState {
		return
	}
	pid := 0
	if p.cmd != nil && p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	if p.config.IsProgram() {
		progName := p.config.GetProgramName()
		groupName := p.config.GetGroupName()
		switch procState {
		case Starting:
			events.EmitEvent(events.CreateProcessStartingEvent(progName, groupName, state.String(), int(p.retryTimes.Load())))
		case Running:
			events.EmitEvent(events.CreateProcessRunningEvent(progName, groupName, state.String(), pid))
		case Backoff:
			events.EmitEvent(events.CreateProcessBackoffEvent(progName, groupName, state.String(), int(p.retryTimes.Load())))
		case Stopping:
			events.EmitEvent(events.CreateProcessStoppingEvent(progName, groupName, state.String(), pid))
		case Exited:
			exitCode, err := p.getExitCode()
			expected := 0
			if err == nil && p.inExitCodes(exitCode) {
				expected = 1
			}
			events.EmitEvent(events.CreateProcessExitedEvent(progName, groupName, state.String(), expected, pid))
		case Fatal:
			events.EmitEvent(events.CreateProcessFatalEvent(progName, groupName, state.String()))
		case Stopped:
			events.EmitEvent(events.CreateProcessStoppedEvent(progName, groupName, state.String(), pid))
		case Unknown:
			events.EmitEvent(events.CreateProcessUnknownEvent(progName, groupName, state.String()))
		}
	}
	p.state.Store(procState)
}

// Signal sends signal to the process
//
// Args:
//
//	sig - the signal to the process
//	sigChildren - if true, sends the same signal to the process and its children
func (p *Process) Signal(sig string, sigChildren bool) error {

	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.sendSignal(sig, sigChildren)
}

func (p *Process) sendSignal(sig string, children bool) error {
	if !p.IsRunning() || p.cmd == nil || p.cmd.Process == nil {
		return fmt.Errorf("process is not started")
	}
	return signals.Send(p.cmd.Process, sig, children)
}

func (p *Process) setEnv() {
	envFromFiles := p.config.GetEnvFromFiles("envFiles")
	env := p.config.GetEnv("environment")
	if len(env)+len(envFromFiles) != 0 {
		p.cmd.Env = mergeKeyValueArrays(mergeKeyValueArrays(os.Environ(), envFromFiles), append(p.cmd.Env, env...))
	} else {
		p.cmd.Env = mergeKeyValueArrays(os.Environ(), p.cmd.Env)
	}
}

// 辅助函数：带覆盖的环境变量追加
func mergeKeyValueArrays(arr1, arr2 []string) []string {
	values := make(map[string]string)
	order := []string{}
	for _, item := range append(append([]string{}, arr1...), arr2...) {
		key, _, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = item
	}
	result := make([]string, 0, len(order))
	for _, key := range order {
		result = append(result, values[key])
	}
	return result
}

func (p *Process) setDir() { p.cmd.Dir = p.workingDirectory() }

func (p *Process) setLog() {
	if p.config.IsProgram() {
		if p.StdoutLog != nil {
			p.cmd.Stdout = p.StdoutLog
			p.cmd.Stderr = p.StderrLog
			return
		}
		p.StdoutLog = p.createStdoutLogger()
		captureBytes := p.config.GetBytes("stdout_capture_maxbytes", 0)
		if captureBytes > 0 {
			log.WithFields(log.Fields{"program": p.config.GetProgramName()}).Info("capture stdout process communication")
			p.StdoutLog = logger.NewLogCaptureLogger(p.StdoutLog,
				captureBytes,
				"PROCESS_COMMUNICATION_STDOUT",
				p.GetName(),
				p.GetGroup())
		}

		p.cmd.Stdout = p.StdoutLog

		if p.config.GetBool("redirect_stderr", false) {
			p.StderrLog = p.StdoutLog
		} else {
			p.StderrLog = p.createStderrLogger()
		}

		captureBytes = p.config.GetBytes("stderr_capture_maxbytes", 0)

		if captureBytes > 0 {
			log.WithFields(log.Fields{"program": p.config.GetProgramName()}).Info("capture stderr process communication")
			p.StderrLog = logger.NewLogCaptureLogger(p.StderrLog,
				captureBytes,
				"PROCESS_COMMUNICATION_STDERR",
				p.GetName(),
				p.GetGroup())
		}

		p.cmd.Stderr = p.StderrLog

	} else if p.config.IsEventListener() {
		in, err := p.cmd.StdoutPipe()
		if err != nil {
			log.WithFields(log.Fields{"eventListener": p.config.GetEventListenerName()}).Error("fail to get stdin")
			return
		}
		out, err := p.cmd.StdinPipe()
		if err != nil {
			log.WithFields(log.Fields{"eventListener": p.config.GetEventListenerName()}).Error("fail to get stdout")
			return
		}
		events := strings.Split(p.config.GetString("events", ""), ",")
		for i, event := range events {
			events[i] = strings.TrimSpace(event)
		}
		p.cmd.Stderr = os.Stderr

		p.registerEventListener(p.config.GetEventListenerName(),
			events,
			in,
			out)
	}
}

func (p *Process) createStdoutLogEventEmitter() logger.LogEventEmitter {
	if p.config.GetBytes("stdout_capture_maxbytes", 0) <= 0 && p.config.GetBool("stdout_events_enabled", false) {
		return logger.NewStdoutLogEventEmitter(p.config.GetProgramName(), p.config.GetGroupName(), func() int {
			return p.GetPid()
		})
	}
	return logger.NewNullLogEventEmitter()
}

func (p *Process) createStderrLogEventEmitter() logger.LogEventEmitter {
	if p.config.GetBytes("stderr_capture_maxbytes", 0) <= 0 && p.config.GetBool("stderr_events_enabled", false) {
		return logger.NewStderrLogEventEmitter(p.config.GetProgramName(), p.config.GetGroupName(), func() int {
			return p.GetPid()
		})
	}
	return logger.NewNullLogEventEmitter()
}

func (p *Process) registerEventListener(eventListenerName string,
	_events []string,
	stdin io.Reader,
	stdout io.Writer) {
	eventListener := events.NewEventListener(eventListenerName,
		p.supervisorID,
		stdin,
		stdout,
		p.config.GetInt("buffer_size", 100))
	events.RegisterEventListener(eventListenerName, _events, eventListener)
}

func (p *Process) createStdoutLogger() logger.Logger {
	logFile := p.GetStdoutLogfile()
	maxBytes := int64(p.config.GetBytes("stdout_logfile_maxbytes", 50*1024*1024))
	backups := p.config.GetInt("stdout_logfile_backups", 10)
	fileNameWithTimestamp := p.config.GetBool("stdout_logfile_timestamp_suffix", true)
	logEventEmitter := p.createStdoutLogEventEmitter()
	props := make(map[string]string)
	syslog_facility := p.config.GetString("syslog_facility", "")
	syslog_tag := p.config.GetString("syslog_tag", "")
	syslog_priority := p.config.GetString("syslog_stdout_priority", "")

	if len(syslog_facility) > 0 {
		props["syslog_facility"] = syslog_facility
	}
	if len(syslog_tag) > 0 {
		props["syslog_tag"] = syslog_tag
	}
	if len(syslog_priority) > 0 {
		props["syslog_priority"] = syslog_priority
	}

	log.WithFields(log.Fields{"program": p.GetName(), "logFile": logFile}).Info("create stdout logger")

	return logger.NewLogger(p.GetName(), logFile, &sync.Mutex{}, maxBytes, backups, fileNameWithTimestamp, props, logEventEmitter)
}

func (p *Process) createStderrLogger() logger.Logger {
	logFile := p.GetStderrLogfile()
	maxBytes := int64(p.config.GetBytes("stderr_logfile_maxbytes", 50*1024*1024))
	backups := p.config.GetInt("stderr_logfile_backups", 10)
	fileNameWithTimestamp := p.config.GetBool("stderr_logfile_timestamp_suffix", true)
	logEventEmitter := p.createStderrLogEventEmitter()
	props := make(map[string]string)
	syslog_facility := p.config.GetString("syslog_facility", "")
	syslog_tag := p.config.GetString("syslog_tag", "")
	syslog_priority := p.config.GetString("syslog_stderr_priority", "")

	if len(syslog_facility) > 0 {
		props["syslog_facility"] = syslog_facility
	}
	if len(syslog_tag) > 0 {
		props["syslog_tag"] = syslog_tag
	}
	if len(syslog_priority) > 0 {
		props["syslog_priority"] = syslog_priority
	}

	return logger.NewLogger(p.GetName(), logFile, &sync.Mutex{}, maxBytes, backups, fileNameWithTimestamp, props, logEventEmitter)
}

func (p *Process) setUser() error {
	userName := p.config.GetString("user", "")
	if len(userName) == 0 {
		return nil
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("program user switching is unsupported on Windows; configure the service account instead")
	}

	// check if group is provided
	pos := strings.Index(userName, ":")
	groupName := ""
	if pos != -1 {
		groupName = userName[pos+1:]
		userName = userName[0:pos]
	}
	u, err := user.Lookup(userName)
	if err != nil {
		return err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return err
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil && groupName == "" {
		return err
	}
	if groupName != "" {
		g, err := user.LookupGroup(groupName)
		if err != nil {
			return err
		}
		gid, err = strconv.ParseUint(g.Gid, 10, 32)
		if err != nil {
			return err
		}
	}
	groupIDs, err := u.GroupIds()
	if err != nil {
		return fmt.Errorf("lookup supplementary groups: %w", err)
	}
	if len(groupIDs) > 1024 {
		return fmt.Errorf("too many supplementary groups")
	}
	groups := make([]uint32, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		value, err := strconv.ParseUint(groupID, 10, 32)
		if err != nil {
			return fmt.Errorf("parse supplementary group: %w", err)
		}
		groups = append(groups, uint32(value))
	}
	setUserID(p.cmd.SysProcAttr, uint32(uid), uint32(gid), groups)

	// 强制设置关键环境变量
	p.cmd.Env = appendEnvWithOverride(p.cmd.Env,
		"HOME", u.HomeDir, // 强制HOME目录
		"USER", u.Username, // 用户名
		"LOGNAME", u.Username, // 登录名
		"PATH", defaultPath(u), // 安全PATH
	)

	// 删除root残留的环境变量
	filterRootEnv(&p.cmd.Env)

	return nil
}

// 辅助函数：带覆盖的环境变量追加
func appendEnvWithOverride(env []string, pairs ...string) []string {
	newEnv := make([]string, 0, len(env)+len(pairs)/2)
	set := make(map[string]bool)

	// 先添加新变量
	for i := 0; i < len(pairs); i += 2 {
		key := pairs[i]
		value := pairs[i+1]
		newEnv = append(newEnv, fmt.Sprintf("%s=%s", key, value))
		set[key] = true
	}

	// 保留未覆盖的旧变量
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) < 2 || set[parts[0]] {
			continue
		}
		newEnv = append(newEnv, e)
	}

	return newEnv
}

// 辅助函数：生成安全PATH
func defaultPath(u *user.User) string {
	// 根据用户类型返回不同PATH
	if u.Uid == "0" {
		return "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	return "/usr/local/bin:/usr/bin:/bin:/usr/local/games:/usr/games"
}

// 辅助函数：过滤危险的环境变量
func filterRootEnv(env *[]string) {
	filtered := make([]string, 0, len(*env))
	for _, e := range *env {
		if strings.HasPrefix(e, "SUDO_") ||
			strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
			continue
		}
		filtered = append(filtered, e)
	}
	*env = filtered
}

// Stop sends signal to process to make it quit

func (p *Process) GetStatus() string {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if p.IsRunning() {
		return "running"
	}
	return fmt.Sprintf("exit status %d", p.lastExitCode)
}
