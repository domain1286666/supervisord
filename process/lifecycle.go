package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ochinchina/supervisord/events"
	"github.com/ochinchina/supervisord/logger"
	"github.com/ochinchina/supervisord/signals"
	log "github.com/sirupsen/logrus"
)

// Start owns one restart loop. A closed ready channel cannot lose a wakeup.
func (p *Process) Start(wait bool) {
	p.lock.Lock()
	if p.inStart || p.closed.Load() {
		p.lock.Unlock()
		return
	}
	p.inStart = true
	p.cmd = nil
	p.retryTimes.Store(1)
	p.changeStateTo(Starting)
	p.stopSent = false
	p.stopByUser.Store(false)
	p.runDone = make(chan struct{})
	p.stopCh = make(chan struct{})
	ready := make(chan struct{})
	p.lock.Unlock()
	go p.runLoop(ready)
	if wait {
		<-ready
	}
}

func (p *Process) runLoop(ready chan struct{}) {
	var readyOnce sync.Once
	announce := func() { readyOnce.Do(func() { close(ready) }) }
	defer announce()
	defer func() { p.lock.Lock(); p.inStart = false; close(p.runDone); p.lock.Unlock() }()
	failures := 0
	for !p.stopByUser.Load() && !p.closed.Load() {
		p.lock.Lock()
		p.startTime = time.Now()
		p.retryTimes.Store(int32(failures + 1))
		p.changeStateTo(Starting)
		err := p.createProgramCommand()
		cmd := p.cmd
		p.lock.Unlock()
		if err == nil {
			p.executePreStartHook()
		}
		p.lock.Lock()
		if p.stopByUser.Load() || p.closed.Load() {
			p.closeStdinLocked()
			p.state.Store(Stopped)
			p.lock.Unlock()
			return
		}
		if err == nil {
			err = StartCommand(cmd)
		}
		if err != nil {
			if p.config.IsEventListener() {
				events.UnregisterEventListener(p.GetName())
			}
			p.closeStdinLocked()
			failures++
			fatal := failures >= max(1, int(p.getStartRetries()))
			if fatal {
				p.changeStateTo(Fatal)
			} else {
				p.changeStateTo(Backoff)
			}
			p.lock.Unlock()
			if fatal {
				log.WithError(err).WithField("program", p.GetName()).Error("fail to start program")
				return
			}
			if !p.retryDelay(time.Duration(min(failures, 10)) * time.Second) {
				return
			}
			continue
		}
		p.runGeneration.Add(1)
		if p.livenessChecker != nil {
			p.livenessChecker.Reset()
		}
		if p.StdoutLog != nil {
			p.StdoutLog.SetPid(cmd.Process.Pid)
		}
		if p.StderrLog != nil {
			p.StderrLog.SetPid(cmd.Process.Pid)
		}
		initiallyRunning := p.getStartSeconds() <= 0 && !p.stopByUser.Load()
		if initiallyRunning {
			p.changeStateTo(Running)
		}
		p.lock.Unlock()
		if initiallyRunning {
			announce()
		}
		exited := make(chan error, 1)
		go func() { exited <- WaitCommand(cmd) }()
		timer := time.NewTimer(time.Duration(max(0, p.getStartSeconds())) * time.Second)
		ran := initiallyRunning
		select {
		case err = <-exited:
		case <-timer.C:
			p.lock.Lock()
			if !p.stopByUser.Load() {
				p.changeStateTo(Running)
				ran = true
			}
			p.lock.Unlock()
			announce()
			err = <-exited
		}
		timer.Stop()
		if err != nil {
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) {
				log.WithError(err).WithField("program", p.GetName()).Warn("process I/O wait failed")
			}
		}
		p.lock.Lock()
		p.stopTime = time.Now()
		cmd.Stdin = nil
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.Env = nil
		if cmd.ProcessState != nil {
			p.lastExitCode = cmd.ProcessState.ExitCode()
		}
		p.closeStdinLocked()
		if p.config.IsEventListener() {
			events.UnregisterEventListener(p.GetName())
		}
		stopped := p.stopByUser.Load() || p.closed.Load()
		if stopped {
			p.changeStateTo(Stopped)
		} else {
			p.changeStateTo(Exited)
		}
		p.lock.Unlock()
		if stopped {
			return
		}
		if ran {
			failures = 0
			if !p.isAutoRestart() {
				return
			}
		} else {
			failures++
			if failures >= max(1, int(p.getStartRetries())) {
				p.lock.Lock()
				p.changeStateTo(Fatal)
				p.lock.Unlock()
				log.WithField("program", p.GetName()).Error("fail to start program before startsecs")
				return
			}
			p.state.Store(Backoff)
		}
		// Backoff is interruptible; even zero startsecs cannot create a busy loop.
		if !p.retryDelay(time.Duration(max(1, p.getRestartPause())) * time.Second) {
			return
		}
	}
	p.state.Store(Stopped)
}

func (p *Process) retryDelay(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-p.stopCh:
		p.state.Store(Stopped)
		return false
	case <-timer.C:
		return true
	}
}

func (p *Process) closeStdinLocked() {
	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
}

// Stop requests graceful shutdown, then escalates after stopwaitsecs.
func (p *Process) Stop(wait bool) { p.stop(false, wait) }

// Kill immediately terminates the process and suppresses automatic restart.
func (p *Process) Kill(wait bool) { p.stop(true, wait) }

func (p *Process) stop(force, wait bool) {
	p.lock.Lock()
	if p.stopByUser == nil {
		p.lock.Unlock()
		return
	}
	if !p.stopByUser.Swap(true) && p.stopCh != nil {
		close(p.stopCh)
	}
	done := p.runDone
	active := p.inStart
	var child *os.Process
	if p.cmd != nil && p.IsRunning() {
		child = p.cmd.Process
	}
	send := !p.stopSent || force
	p.stopSent = true
	if active && child != nil && p.IsRunning() {
		p.changeStateTo(Stopping)
	}
	p.lock.Unlock()
	if !active {
		return
	}
	if child != nil && send {
		go func() {
			if !force {
				p.executePreStopHook()
			}
			sigs := strings.Fields(p.config.GetString("stopsignal", "TERM"))
			if force {
				sigs = []string{"KILL"}
			}
			stopGroup := p.config.GetBool("stopasgroup", false)
			killGroup := p.config.GetBool("killasgroup", stopGroup)
			for _, sig := range sigs {
				children := stopGroup
				if force {
					children = killGroup
				}
				if err := signals.Send(child, sig, children); err != nil {
					log.WithError(err).WithField("program", p.GetName()).Debug("send stop signal")
				}
				if force {
					return
				}
				timer := time.NewTimer(time.Duration(max(0, p.config.GetInt("stopwaitsecs", 10))) * time.Second)
				select {
				case <-done:
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			if err := signals.Send(child, "KILL", killGroup); err != nil {
				log.WithError(err).Warn("force stop failed")
			}
		}()
	}
	if wait {
		budget := max(0, p.config.GetInt("stopwaitsecs", 10))*max(1, len(strings.Fields(p.config.GetString("stopsignal", "TERM")))) + 10
		timer := time.NewTimer(time.Duration(budget) * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			log.WithField("program", p.GetName()).Error("stop exceeded deadline")
		}
	}
}

// Close releases cron registration, file watches and log descriptors.
func (p *Process) Close() {
	if p.closed.Swap(true) {
		return
	}
	if p.cronID != 0 {
		scheduler.Remove(p.cronID)
	}
	if p.livenessChecker != nil {
		p.livenessChecker.scriptExecutor.Close()
	}
	p.Stop(true)
	if p.livenessChecker != nil {
		p.livenessChecker.Close()
	}
	p.lock.Lock()
	cancel := p.watchCancel
	p.watchCancel = nil
	stdout, stderr := p.StdoutLog, p.StderrLog
	p.lock.Unlock()
	if cancel != nil {
		cancel()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	if stderr != nil && stderr != stdout {
		_ = stderr.Close()
	}
}

// Logs returns stable, synchronized log handles, including after process exit.
func (p *Process) Logs() (logger.Logger, logger.Logger) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.StdoutLog, p.StderrLog
}

// ClearLogs clears each distinct stream and preserves filesystem failures.
func (p *Process) ClearLogs() error {
	stdout, stderr := p.Logs()
	var failures []error
	if stdout != nil {
		if err := stdout.ClearAllLogFile(); err != nil {
			failures = append(failures, fmt.Errorf("stdout: %w", err))
		}
	}
	if stderr != nil && stderr != stdout {
		if err := stderr.ClearAllLogFile(); err != nil {
			failures = append(failures, fmt.Errorf("stderr: %w", err))
		}
	}
	return errors.Join(failures...)
}

func (p *Process) workingDirectory() string {
	if p.workingDir != "" {
		return p.workingDir
	}
	dir := p.config.GetStringExpression("directory", "")
	if dir == "" {
		dir = p.config.ConfigDir
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(p.config.ConfigDir, dir)
	}
	if expanded, err := PathExpand(dir); err == nil {
		dir = expanded
	}
	if absolute, err := filepath.Abs(dir); err == nil {
		return absolute
	}
	return dir
}

func (p *Process) resolveLogPaths(value string) string {
	paths := strings.Split(value, ",")
	for i, name := range paths {
		name = strings.TrimSpace(name)
		switch name {
		case "AUTO", "NONE", "memory", "syslog", "", "/dev/null", "/dev/stdout", "/dev/stderr":
			paths[i] = name
			continue
		}
		if strings.HasPrefix(name, "syslog ") {
			paths[i] = name
			continue
		}
		expanded, err := PathExpand(name)
		if err != nil {
			expanded = name
		}
		if !filepath.IsAbs(expanded) {
			expanded = filepath.Join(p.workingDirectory(), expanded)
		}
		paths[i] = expanded
	}
	return strings.Join(paths, ",")
}

// ValidateCommand validates configuration without starting a process.
func ValidateCommand(command string) error {
	_, err := parseCommand(command)
	if err != nil {
		return fmt.Errorf("command: %w", err)
	}
	return nil
}
