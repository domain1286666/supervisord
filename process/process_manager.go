package process

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ochinchina/supervisord/config"
	log "github.com/sirupsen/logrus"
)

// Manager manage all the process in the supervisor
type Manager struct {
	procs          map[string]*Process
	eventListeners map[string]*Process
	lock           sync.Mutex
	done           chan struct{}
	closed         sync.Once
	wg             sync.WaitGroup
}

// NewManager creates new Manager object
func NewManager() *Manager {
	manager := &Manager{procs: make(map[string]*Process),
		eventListeners: make(map[string]*Process),
		done:           make(chan struct{}),
	}
	manager.startLivenessCheckers()
	return manager
}

// CreateProcess creates process (program or event listener) and adds to Manager object
func (pm *Manager) CreateProcess(supervisorID string, config *config.Entry) *Process {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	if config.IsProgram() {
		return pm.createProgram(supervisorID, config)
	} else if config.IsEventListener() {
		return pm.createEventListener(supervisorID, config)
	} else {
		return nil
	}
}

// StartAutoStartPrograms starts all programs that set as should be autostarted
func (pm *Manager) StartAutoStartPrograms() {
	pm.StartAutoStartProgramsMatching(nil)
}

// StartAutoStartProgramsMatching starts configured programs accepted by allow.
// A nil predicate selects all programs, preserving full reload behavior.
func (pm *Manager) StartAutoStartProgramsMatching(allow func(*Process) bool) {
	pm.ForEachProcess(func(proc *Process) {
		if proc.isAutoStart() && (allow == nil || allow(proc)) {
			proc.Start(false)
		}
	})
}

func (pm *Manager) createProgram(supervisorID string, config *config.Entry) *Process {
	procName := config.GetProgramName()

	proc, ok := pm.procs[procName]

	if !ok {
		proc = NewProcess(supervisorID, config)
		pm.procs[procName] = proc
	}
	log.Info("create process:", procName)
	return proc
}

func (pm *Manager) createEventListener(supervisorID string, config *config.Entry) *Process {
	eventListenerName := config.GetEventListenerName()

	evtListener, ok := pm.eventListeners[eventListenerName]

	if !ok {
		evtListener = NewProcess(supervisorID, config)
		pm.eventListeners[eventListenerName] = evtListener
	}
	log.Info("create event listener:", eventListenerName)
	return evtListener
}

// Add process to Manager object
func (pm *Manager) Add(name string, proc *Process) {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	pm.procs[name] = proc
	log.Info("add process:", name)
}

// Remove process from Manager object
//
// Arguments:
// name - the name of program
//
// Return the process or nil
func (pm *Manager) Remove(name string) *Process {
	pm.lock.Lock()
	proc := pm.procs[name]
	delete(pm.procs, name)
	pm.lock.Unlock()
	if proc != nil {
		proc.Close()
	}
	return proc
}

// RemoveEventListener releases the listener and its child resources.
func (pm *Manager) RemoveEventListener(name string) {
	pm.lock.Lock()
	proc := pm.eventListeners[name]
	delete(pm.eventListeners, name)
	pm.lock.Unlock()
	if proc != nil {
		proc.Close()
	}
}

func (pm *Manager) Find(name string) *Process {
	procs := pm.FindMatch(name)
	if len(procs) == 1 {
		if procs[0].GetName() == name || name == fmt.Sprintf("%s:%s", procs[0].GetGroup(), procs[0].GetName()) {
			return procs[0]
		}
	}
	return nil
}

// FindMatch lookup program with one of following format:
// - group:program
// - group:*
// - program
func (pm *Manager) FindMatch(name string) []*Process {
	result := make([]*Process, 0)
	if before, after, ok := strings.Cut(name, ":"); ok {
		groupName := before
		programName := after
		pm.ForEachProcess(func(p *Process) {
			if p.GetGroup() == groupName {
				if programName == "*" || programName == p.GetName() {
					result = append(result, p)
				}
			}
		})
	} else {
		pm.lock.Lock()
		defer pm.lock.Unlock()
		proc, ok := pm.procs[name]
		if ok {
			result = append(result, proc)
		}
	}
	if len(result) <= 0 {
		log.Info("fail to find process:", name)
	}
	return result
}

// Clear all the processes from Manager object
func (pm *Manager) Clear() {
	pm.lock.Lock()
	procs := pm.procs
	listeners := pm.eventListeners
	pm.procs = make(map[string]*Process)
	pm.eventListeners = make(map[string]*Process)
	pm.lock.Unlock()
	for _, proc := range procs {
		proc.Close()
	}
	for _, proc := range listeners {
		proc.Close()
	}
}

func (pm *Manager) ForEachProcess(procFunc func(p *Process)) {
	procs := pm.getAllProcess()
	for _, proc := range sortProcess(procs) {
		procFunc(proc)
	}
}

// AsyncForEachProcess handle each process in async mode
// Args:
// - procFunc, the function to handle the process
// - done, signal the process is completed
// Returns: number of total processes
func (pm *Manager) AsyncForEachProcess(procFunc func(p *Process), done chan *Process) int {
	procs := pm.getAllProcess()

	for _, proc := range sortProcess(procs) {
		go forOneProcess(proc, procFunc, done)
	}
	return len(procs)
}

func forOneProcess(proc *Process, action func(p *Process), done chan *Process) {
	action(proc)
	done <- proc
}

func (pm *Manager) getAllProcess() []*Process {
	pm.lock.Lock()
	defer pm.lock.Unlock()
	tmpProcs := make([]*Process, 0, len(pm.procs))
	for _, proc := range pm.procs {
		tmpProcs = append(tmpProcs, proc)
	}
	return tmpProcs
}

// StopAllProcesses stop all the processes listed in Manager object
func (pm *Manager) StopAllProcesses() {
	var wg sync.WaitGroup

	pm.ForEachProcess(func(proc *Process) {
		wg.Add(1)

		go func(wg *sync.WaitGroup) {
			defer wg.Done()

			proc.Stop(true)
		}(&wg)
	})

	wg.Wait()
}

func (pm *Manager) IsAllProcessesStopped() bool {
	procs := pm.getAllProcess()

	for _, proc := range sortProcess(procs) {
		if proc.IsRunning() {
			return false
		}
	}
	log.Info("all processes are stopped")
	return true
}

func (pm *Manager) startLivenessCheckers() {
	pm.wg.Add(1)
	go func() {
		defer pm.wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pm.done:
				return
			case <-ticker.C:
				pm.ForEachProcess(func(proc *Process) { proc.DoLivenessCheck() })
			}
		}
	}()
}

// Close stops health scheduling and releases every owned process.
func (pm *Manager) Close() {
	pm.closed.Do(func() { close(pm.done) })
	pm.wg.Wait()
	pm.StopAllProcesses()
	pm.ForEachProcess(func(proc *Process) { proc.Close() })
	pm.lock.Lock()
	listeners := pm.eventListeners
	pm.eventListeners = make(map[string]*Process)
	pm.lock.Unlock()
	for _, proc := range listeners {
		proc.Close()
	}
}

func sortProcess(procs []*Process) []*Process {
	entries := make([]*config.Entry, 0, len(procs))
	byEntry := make(map[*config.Entry]*Process, len(procs))
	for _, proc := range procs {
		if proc.config != nil && proc.config.IsProgram() {
			entries = append(entries, proc.config)
			byEntry[proc.config] = proc
		}
	}
	ordered := config.NewProcessSorter().SortProgram(entries)
	result := make([]*Process, 0, len(ordered))
	for _, entry := range ordered {
		result = append(result, byEntry[entry])
	}
	return result
}
