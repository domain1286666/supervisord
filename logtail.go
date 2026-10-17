package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ochinchina/supervisord/logger"

	"github.com/gorilla/mux"
)

// Logtail tails the process log through http interface
type Logtail struct {
	router     *mux.Router
	supervisor *Supervisor
}

// NewLogtail creates a Logtail object
func NewLogtail(supervisor *Supervisor) *Logtail {
	return &Logtail{router: mux.NewRouter(), supervisor: supervisor}
}

// CreateHandler creates http handlers to process the program stdout and stderr through http interface
func (lt *Logtail) CreateHandler() http.Handler {
	lt.router.HandleFunc("/logtail/{program}/stdout", lt.getStdoutLog).Methods("GET")
	lt.router.HandleFunc("/logtail/{program}/stderr", lt.getStderrLog).Methods("GET")
	return lt.router
}

func (lt *Logtail) getStdoutLog(w http.ResponseWriter, req *http.Request) {
	lt.getLog("stdout", w, req)
}

func (lt *Logtail) getStderrLog(w http.ResponseWriter, req *http.Request) {
	lt.getLog("stderr", w, req)
}

func (lt *Logtail) getLog(logType string, w http.ResponseWriter, req *http.Request) {
	w = withStreamDeadline(w, req)
	vars := mux.Vars(req)
	program := vars["program"]
	procMgr := lt.supervisor.GetManager()
	proc := procMgr.Find(program)

	if proc == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	stdout, stderr := proc.Logs()
	var logHandle logger.Logger
	if logType == "stdout" {
		logHandle = stdout
	} else {
		logHandle = stderr
	}
	if logHandle == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	s, offset, _, err := logHandle.ReadTailLog(0, 1600)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write([]byte(s))
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if controller.Flush() != nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-ticker.C:
			text, next, _, err := logHandle.ReadTailLog(offset, 64*1024)
			if err != nil {
				return
			}
			offset = next
			_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err = w.Write([]byte(text)); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		}
	}
}
