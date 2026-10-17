package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error { defer b.cancel(); return b.ReadCloser.Close() }

func (a *consoleAPI) peer(name string) (*remotePeer, error) {
	node, ok := a.remotes.remoteSupervisors.GetNode(name)
	if !ok {
		return nil, fmt.Errorf("remote node is not configured")
	}
	return node.connection()
}

func (a *consoleAPI) remoteClear(w http.ResponseWriter, r *http.Request, node, name, stream string) {
	peer, err := a.peer(node)
	if err != nil {
		webError(w, 404, "远程节点不存在")
		return
	}
	data, _ := json.Marshal(map[string]string{"name": name, "stream": stream})
	response, err := peer.do(r.Context(), "POST", "/api/logs/clear", data, false)
	if err != nil {
		webError(w, 502, "无法连接远程节点")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		webError(w, 502, "远程日志无法清空")
		return
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

func (a *consoleAPI) remoteAction(w http.ResponseWriter, r *http.Request, node, name, action string) {
	peer, err := a.peer(node)
	if err != nil {
		webError(w, 404, "远程节点不存在或证书配置无效")
		return
	}
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		webError(w, 429, "操作队列已满")
		return
	}
	data, _ := json.Marshal(map[string]string{"name": name, "action": action})
	response, err := peer.do(r.Context(), "POST", "/api/process/action", data, false)
	if err != nil {
		webError(w, 502, "无法连接远程节点")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 202 {
		webError(w, 502, "远程节点未接受操作，请检查节点状态和登录凭据")
		return
	}
	writeJSON(w, 202, map[string]bool{"accepted": true})
}

func (a *consoleAPI) remoteLog(w http.ResponseWriter, r *http.Request, stream bool) bool {
	node := r.URL.Query().Get("node")
	if node == "" || node == a.supervisor.getNodeName() {
		return false
	}
	peer, err := a.peer(node)
	if err != nil {
		webError(w, 404, "远程节点不存在或证书配置无效")
		return true
	}
	query := url.Values{"name": {r.URL.Query().Get("name")}, "stream": {r.URL.Query().Get("stream")}}
	if !stream {
		if r.URL.Query().Get("download") == "1" {
			query.Set("download", "1")
		}
		response, err := peer.do(r.Context(), "GET", "/api/logs?"+query.Encode(), nil, false)
		if err != nil {
			webError(w, 502, "无法读取远程日志")
			return true
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		if query.Get("download") == "1" {
			w.Header().Set("Content-Disposition", `attachment; filename="remote-log.txt"`)
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(response.Body, 1<<20))
		return true
	}
	if !a.streamSlot(w) {
		return true
	}
	defer func() { <-a.streams }()
	sseHeaders(w)
	delay := time.Second
	for {
		if _, ok := a.auth.session(r); !ok {
			_ = sendEvent(w, "session-expired", []byte(`{}`))
			return true
		}
		err := readRemoteEvents(r.Context(), peer, "/api/logs/events?"+query.Encode(), func(event string, data []byte) error {
			if _, ok := a.auth.session(r); !ok {
				return fmt.Errorf("local session expired")
			}
			return sendEvent(w, event, data)
		})
		if r.Context().Err() != nil || a.ctx.Err() != nil {
			return true
		}
		if err != nil {
			if err = sendEvent(w, "upstream-reconnecting", []byte(`{}`)); err != nil {
				return true
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return true
		case <-a.ctx.Done():
			timer.Stop()
			return true
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}
