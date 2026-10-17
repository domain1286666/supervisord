package main

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// SupervisorWebgui the interface to show a WEBGUI to control the supervisor
type SupervisorWebgui struct {
	router     *mux.Router
	supervisor *Supervisor
}

// NewSupervisorWebgui create a new SupervisorWebgui object
func NewSupervisorWebgui(supervisor *Supervisor) *SupervisorWebgui {
	router := mux.NewRouter()
	return &SupervisorWebgui{router: router, supervisor: supervisor}
}

// CreateHandler create a http handler to process the request from WEBGUI
func (sw *SupervisorWebgui) CreateHandler() http.Handler {
	files := http.FileServer(HTTP)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		path := r.URL.Path
		if strings.HasPrefix(path, "/assets/") {
			files.ServeHTTP(w, r)
			return
		}
		switch path {
		case "/", "/login", "/overview", "/processes", "/logs", "/settings", "/configuration", "/log", "/confFile":
			request := r.Clone(r.Context())
			url := *r.URL
			url.Path = "/"
			request.URL = &url
			files.ServeHTTP(w, request)
		default:
			if strings.HasPrefix(path, "/log/") {
				request := r.Clone(r.Context())
				url := *r.URL
				url.Path = "/"
				request.URL = &url
				files.ServeHTTP(w, request)
				return
			}
			http.NotFound(w, r)
		}
	})
}
