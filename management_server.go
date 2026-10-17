package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ochinchina/supervisord/process"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/netutil"
)

func (p *XMLRPC) serveManagement(protocol, addr string, s *Supervisor, remotes map[string]*NodeLoginInfo) error {
	ctx, cancel := context.WithCancel(context.Background())
	mux := http.NewServeMux()
	var handler http.Handler
	var certificate tls.Certificate
	var api *consoleAPI
	if protocol == "unix" {
		// Local RPC authority is the socket's 0600 filesystem permission.
		if info, err := os.Lstat(addr); err == nil {
			if info.Mode()&os.ModeSocket == 0 {
				cancel()
				return errors.New("refusing to replace non-socket path")
			}
			if err = os.Remove(addr); err != nil {
				cancel()
				return err
			}
		}
		mux.Handle("/RPC2", p.createRPCServer(s))
		handler = mux
	} else {
		auth, generated, err := prepareWebSecurity(s.GetConfig(), false)
		if err != nil {
			cancel()
			return err
		}
		if generated != "" {
			fmt.Fprintf(os.Stderr, "\nHTTPS administrator: %s\nInitial password: %s\nStore these credentials; the password is not saved in plaintext.\n\n", auth.username, generated)
		}
		certificate = auth.certificate
		mux.HandleFunc("POST /api/auth/login", auth.login)
		mux.HandleFunc("GET /api/auth/session", auth.current)
		api = newConsoleAPI(ctx, s, auth)
		mux.Handle("/api/", api.routes())
		mux.Handle("/RPC2", auth.protectRPC(p.createRPCServer(s)))
		restful := api.remotes
		mux.Handle("/program/", auth.protect(restful.CreateProgramHandler()))
		mux.Handle("/supervisor/", auth.protect(restful.CreateSupervisorHandler()))
		mux.Handle("/logtail/", auth.protect(NewLogtail(s).CreateHandler()))
		registry := prometheus.NewRegistry()
		registry.MustRegister(process.NewProcCollector(s.procMgr), collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
		mux.Handle("/metrics", auth.protect(promhttp.HandlerFor(registry, promhttp.HandlerOpts{})))
		mux.Handle("/", NewSupervisorWebgui(s).CreateHandler())
		handler, err = secureHandler(mux)
		if err != nil {
			cancel()
			return fmt.Errorf("initialize Coraza WAF: %w", err)
		}
	}
	if protocol == "tcp" && !strings.Contains(addr, ":") {
		addr = ":" + addr
	}
	listener, err := net.Listen(protocol, addr)
	if err != nil {
		cancel()
		return err
	}
	listener = netutil.LimitListener(listener, 256)
	if protocol == "unix" {
		if err = os.Chmod(addr, 0600); err != nil {
			listener.Close()
			cancel()
			return err
		}
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024, BaseContext: func(net.Listener) context.Context { return ctx }}
	if protocol == "tcp" {
		server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}, NextProtos: []string{"h2", "http/1.1"}}
	}
	p.mu.Lock()
	p.listeners[protocol] = listener
	p.servers[protocol] = server
	p.cancels[protocol] = func() {
		cancel()
		if api != nil {
			api.Close()
		}
	}
	p.mu.Unlock()
	go func() {
		var err error
		if protocol == "tcp" {
			err = server.ServeTLS(listener, "", "")
		} else {
			err = server.Serve(listener)
		}
		cancel()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.WithError(err).Error("management listener failed")
		}
	}()
	log.WithFields(log.Fields{"address": listener.Addr().String(), "protocol": protocol}).Info("management server ready")
	return nil
}
