package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/ochinchina/supervisord/logger"
	"github.com/ochinchina/supervisord/process"
	"github.com/ochinchina/supervisord/types"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

type consoleAPI struct {
	wg            sync.WaitGroup
	remotes       *SupervisorRestful
	remoteState   map[string]remoteSnapshot
	localSnapshot []byte
	supervisor    *Supervisor
	auth          *webSecurity
	ctx           context.Context
	mu            sync.Mutex
	subscribers   map[chan []byte]bool
	snapshot      []byte
	jobs          map[string]bool
	closing       bool
	slots         chan struct{}
	streams       chan struct{}
}

func newConsoleAPI(ctx context.Context, s *Supervisor, auth *webSecurity) *consoleAPI {
	api := &consoleAPI{supervisor: s, auth: auth, ctx: ctx, subscribers: make(map[chan []byte]bool), jobs: make(map[string]bool), slots: make(chan struct{}, 8), streams: make(chan struct{}, 64)}
	api.remotes = NewSupervisorRestful(s).AddRemoteSupervisors(s.getRemoteNodes())
	api.remoteState = make(map[string]remoteSnapshot)
	for name, node := range api.remotes.remoteSupervisors.GetAllNode() {
		api.remoteState[name] = remoteSnapshot{Node: name}
		api.wg.Add(1)
		go func() { defer api.wg.Done(); api.followRemote(name, node) }()
	}
	api.wg.Add(1)
	go func() { defer api.wg.Done(); api.sampleLoop() }()
	return api
}

func (a *consoleAPI) Close() {
	a.mu.Lock()
	a.closing = true
	a.mu.Unlock()
	a.wg.Wait()
}

func (a *consoleAPI) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/processes", a.processes)
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		_, local := a.samplePair()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(local)
	})
	mux.HandleFunc("POST /api/process/action", a.action)
	mux.HandleFunc("GET /api/events", a.events)
	mux.HandleFunc("GET /api/logs", a.logs)
	mux.HandleFunc("GET /api/logs/events", a.logEvents)
	mux.HandleFunc("POST /api/logs/clear", a.clearLog)
	mux.HandleFunc("GET /api/config", a.configuration)
	mux.HandleFunc("POST /api/config/reload", a.reload)
	mux.HandleFunc("POST /api/auth/logout", a.auth.logout)
	mux.HandleFunc("POST /api/auth/password", a.auth.changePassword)
	return a.auth.protect(mux)
}

func (a *consoleAPI) processList() []types.ProcessInfo {
	node := a.supervisor.getNodeName()
	result := make([]types.ProcessInfo, 0)
	a.supervisor.procMgr.ForEachProcess(func(p *process.Process) { result = append(result, *getProcessInfo(node, p)) })
	types.SortProcessInfos(result)
	return result
}

func (a *consoleAPI) processes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.processList())
}

func (a *consoleAPI) samplePair() ([]byte, []byte) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result := map[string]any{"processes": a.processList(), "node": a.supervisor.getNodeName(), "timestamp": time.Now().Unix(), "runtime": map[string]any{"goroutines": runtime.NumGoroutine(), "heap_bytes": memory.HeapAlloc}, "version": version}
	result["configuration_error"] = ""
	if message := a.supervisor.reloadError.Load(); message != nil {
		result["configuration_error"] = *message
	}
	if values, err := cpu.PercentWithContext(a.ctx, 0, false); err == nil && len(values) > 0 {
		result["cpu_percent"] = values[0]
	}
	if memory, err := mem.VirtualMemoryWithContext(a.ctx); err == nil {
		result["memory_percent"] = memory.UsedPercent
		result["memory_total"] = memory.Total
	}
	local, _ := json.Marshal(result)
	nodes := []map[string]any{{"name": a.supervisor.getNodeName(), "online": true}}
	all := append(make([]types.ProcessInfo, 0), result["processes"].([]types.ProcessInfo)...)
	a.mu.Lock()
	for name, snapshot := range a.remoteState {
		nodes = append(nodes, map[string]any{"name": name, "online": snapshot.Online})
		all = append(all, snapshot.Processes...)
	}
	a.mu.Unlock()
	result["processes"] = all
	result["nodes"] = nodes
	data, _ := json.Marshal(result)
	return data, local
}

func (a *consoleAPI) sampleLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			needed := len(a.subscribers) > 0
			a.mu.Unlock()
			if !needed {
				continue
			}
			data, local := a.samplePair()
			a.mu.Lock()
			a.snapshot = data
			a.localSnapshot = local
			for subscriber, localOnly := range a.subscribers {
				payload := data
				if localOnly {
					payload = local
				}
				select {
				case subscriber <- payload:
				default:
					select {
					case <-subscriber:
					default:
					}
					select {
					case subscriber <- payload:
					default:
					}
				}
			}
			a.mu.Unlock()
		}
	}
}

func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
}
func sendEvent(w http.ResponseWriter, event string, data []byte) error {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	return controller.Flush()
}
func (a *consoleAPI) streamSlot(w http.ResponseWriter) bool {
	select {
	case a.streams <- struct{}{}:
		return true
	default:
		webError(w, 429, "实时连接数量已达上限")
		return false
	}
}

func (a *consoleAPI) events(w http.ResponseWriter, r *http.Request) {
	w = withStreamDeadline(w, r)
	if !a.streamSlot(w) {
		return
	}
	defer func() { <-a.streams }()
	updates := make(chan []byte, 1)
	a.mu.Lock()
	localOnly := r.URL.Query().Get("local") == "1"
	a.subscribers[updates] = localOnly
	initial := a.snapshot
	if localOnly {
		initial = a.localSnapshot
	}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.subscribers, updates); a.mu.Unlock() }()
	if len(initial) == 0 {
		full, local := a.samplePair()
		initial = full
		if localOnly {
			initial = local
		}
	}
	sseHeaders(w)
	if err := sendEvent(w, "snapshot", initial); err != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-a.ctx.Done():
			return
		case data := <-updates:
			if _, valid := a.auth.session(r); !valid {
				_ = sendEvent(w, "session-expired", []byte(`{}`))
				return
			}
			if err := sendEvent(w, "snapshot", data); err != nil {
				return
			}
		case <-heartbeat.C:
			if _, valid := a.auth.session(r); !valid {
				_ = sendEvent(w, "session-expired", []byte(`{}`))
				return
			}
			if err := sendEvent(w, "heartbeat", []byte(`{}`)); err != nil {
				return
			}
		}
	}
}

func (a *consoleAPI) action(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Action string `json:"action"`
		Node   string `json:"node"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		webError(w, 400, "操作数据无效")
		return
	}
	if body.Action != "start" && body.Action != "stop" && body.Action != "restart" && body.Action != "kill" {
		webError(w, 400, "不支持的操作")
		return
	}
	if body.Node != "" && body.Node != a.supervisor.getNodeName() {
		a.remoteAction(w, r, body.Node, body.Name, body.Action)
		return
	}
	p := a.supervisor.procMgr.Find(body.Name)
	if p == nil {
		webError(w, 404, "程序不存在")
		return
	}
	a.mu.Lock()
	if a.closing || a.ctx.Err() != nil {
		a.mu.Unlock()
		webError(w, 503, "管理服务正在重新加载")
		return
	}
	if a.jobs[body.Name] {
		if body.Action == "kill" {
			a.mu.Unlock()
			p.Kill(false)
			writeJSON(w, 202, map[string]bool{"accepted": true})
			return
		}
		a.mu.Unlock()
		webError(w, 409, "程序操作进行中")
		return
	}
	select {
	case a.slots <- struct{}{}:
		a.jobs[body.Name] = true
	default:
		a.mu.Unlock()
		webError(w, 429, "操作队列已满")
		return
	}
	a.wg.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.wg.Done()
		defer func() { <-a.slots; a.mu.Lock(); delete(a.jobs, body.Name); a.mu.Unlock() }()
		select {
		case <-a.ctx.Done():
			return
		default:
		}
		switch body.Action {
		case "start":
			p.Start(false)
		case "stop":
			p.Stop(true)
		case "kill":
			p.Kill(true)
		case "restart":
			p.Stop(true)
			select {
			case <-a.ctx.Done():
				return
			default:
				p.Start(false)
			}
		}
	}()
	writeJSON(w, 202, map[string]bool{"accepted": true})
}

func (a *consoleAPI) logHandle(w http.ResponseWriter, r *http.Request) (logger.Logger, bool) {
	p := a.supervisor.procMgr.Find(r.URL.Query().Get("name"))
	if p == nil {
		webError(w, 404, "程序不存在")
		return nil, false
	}
	stdout, stderr := p.Logs()
	switch r.URL.Query().Get("stream") {
	case "", "stdout":
		return stdout, true
	case "stderr":
		return stderr, true
	default:
		webError(w, 400, "日志类型须为 stdout 或 stderr")
		return nil, false
	}
}

func (a *consoleAPI) logs(w http.ResponseWriter, r *http.Request) {
	if a.remoteLog(w, r, false) {
		return
	}
	handle, ok := a.logHandle(w, r)
	if !ok {
		return
	}
	data, err := logger.StreamSnapshot(handle)
	if err != nil {
		webError(w, 400, "该日志不可读取；请检查日志类型和文件权限")
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="supervisord-log.txt"`)
		_, _ = w.Write([]byte(data))
		return
	}
	writeJSON(w, 200, map[string]string{"text": data})
}

func (a *consoleAPI) logEvents(w http.ResponseWriter, r *http.Request) {
	w = withStreamDeadline(w, r)
	if a.remoteLog(w, r, true) {
		return
	}
	if _, ok := a.logHandle(w, r); !ok {
		return
	}
	if !a.streamSlot(w) {
		return
	}
	defer func() { <-a.streams }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	var previous logger.Logger
	var offset int64
	var generation uint64
	first := true
	push := func() bool {
		if _, valid := a.auth.session(r); !valid {
			_ = sendEvent(w, "session-expired", []byte(`{}`))
			return false
		}
		p := a.supervisor.procMgr.Find(r.URL.Query().Get("name"))
		if p == nil {
			_ = sendEvent(w, "unavailable", []byte(`{"message":"程序已移除"}`))
			return false
		}
		stdout, stderr := p.Logs()
		current := stdout
		if r.URL.Query().Get("stream") == "stderr" {
			current = stderr
		}
		reset := first || current != previous || logger.LogVersion(current) != generation
		if reset {
			offset = 0
		}
		text := ""
		var overflow bool
		var err error
		if current != nil {
			text, offset, overflow, err = current.ReadTailLog(offset, 64*1024)
		}
		if err != nil {
			payload, _ := json.Marshal(map[string]string{"message": "该日志不可读取；请检查日志配置"})
			_ = sendEvent(w, "unavailable", payload)
			return false
		}
		if reset || text != "" {
			payload, _ := json.Marshal(map[string]any{"text": text, "reset": reset || overflow, "offset": offset})
			if err := sendEvent(w, "log", payload); err != nil {
				return false
			}
		}
		previous = current
		generation = logger.LogVersion(current)
		first = false
		return true
	}
	sseHeaders(w)
	if !push() {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if !push() {
				return
			}
		case <-heartbeat.C:
			if err := sendEvent(w, "heartbeat", []byte(`{}`)); err != nil {
				return
			}
		}
	}
}

func (a *consoleAPI) clearLog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Stream string `json:"stream"`
		Node   string `json:"node"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		webError(w, 400, "请求数据无效")
		return
	}
	if body.Node != "" && body.Node != a.supervisor.getNodeName() {
		a.remoteClear(w, r, body.Node, body.Name, body.Stream)
		return
	}
	query := r.URL.Query()
	query.Set("name", body.Name)
	query.Set("stream", body.Stream)
	r.URL.RawQuery = query.Encode()
	handle, ok := a.logHandle(w, r)
	if !ok {
		return
	}
	if handle != nil {
		if err := handle.ClearAllLogFile(); err != nil {
			webError(w, 400, "日志不支持清空或文件不可写")
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

func (a *consoleAPI) configuration(w http.ResponseWriter, r *http.Request) {
	// Never send password hashes, TLS keys, environment values or remote credentials.
	entries := make([]map[string]any, 0)
	for _, p := range a.supervisor.GetConfig().GetPrograms() {
		entries = append(entries, map[string]any{"name": p.GetProgramName(), "command": p.GetStringExpression("command", ""), "directory": p.GetStringExpression("directory", p.ConfigDir), "autostart": p.GetBool("autostart", true), "autorestart": p.GetString("autorestart", "unexpected"), "stopsignal": p.GetString("stopsignal", "TERM"), "stopwaitsecs": p.GetInt("stopwaitsecs", 10)})
	}
	listenAddress := ":8002"
	if entry, ok := a.supervisor.GetConfig().GetInetHTTPServer(); ok {
		listenAddress = entry.GetString("port", listenAddress)
	}
	fingerprint := sha256.Sum256(a.auth.certificate.Certificate[0])
	writeJSON(w, 200, map[string]any{"file": a.supervisor.GetConfig().GetConfigFile(), "programs": entries, "listen_address": listenAddress, "https": true, "waf": "Coraza + OWASP CRS", "protocol": r.Proto, "certificate_sha256": fmt.Sprintf("%x", fingerprint), "certificate_expires_at": a.auth.certificate.Leaf.NotAfter.Unix()})
}

func (a *consoleAPI) reload(w http.ResponseWriter, r *http.Request) {
	if !a.supervisor.reloadPending.CompareAndSwap(false, true) {
		webError(w, 409, "已有配置重载任务正在执行")
		return
	}
	writeJSON(w, 202, map[string]bool{"accepted": true})
	go func() {
		defer a.supervisor.reloadPending.Store(false)
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-a.ctx.Done():
			return
		case <-timer.C:
		}
		_ = a.supervisor.Reload(RestartChanged)
	}()
}
