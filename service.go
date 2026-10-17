package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"

	"github.com/kardianos/service"
	"github.com/ochinchina/supervisord/logger"
	log "github.com/sirupsen/logrus"
)

// ServiceCommand install/uninstall/start/stop supervisord service
type ServiceCommand struct {
}

var serviceCommand ServiceCommand

type program struct {
	supervisor atomic.Pointer[Supervisor]
	service    service.Service
}

func NewProgram() *program {
	return &program{}
}

// Start supervised service
func (p *program) Start(s service.Service) error {
	log.SetOutput(logger.NewReformatLog(os.Stdout))
	supervisor, err := initServer()
	if err != nil {
		return err
	}
	p.supervisor.Store(supervisor)
	p.service = s
	go p.run()
	return nil
}

func (p *program) run() {
	p.supervisor.Load().WaitForExit()
	if p.service != nil && !service.Interactive() {
		_ = p.service.Stop()
	}
}

func (p *program) Stop(s service.Service) error {
	if supervisor := p.supervisor.Load(); supervisor != nil {
		reply := struct{ Ret bool }{}
		return supervisor.Shutdown(nil, nil, &reply)
	}
	return nil
}

func (sc ServiceCommand) Execute(args []string) error {
	if len(args) == 0 {
		showUsage()
		return nil
	}

	serviceArgs := make([]string, 0)
	if options.Configuration != "" {
		serviceArgs = append(serviceArgs, "--configuration="+options.Configuration)
	}
	if options.EnvFile != "" {
		serviceArgs = append(serviceArgs, "--env-file="+options.EnvFile)
	}

	svcConfig := &service.Config{
		Name:        "go-supervisord",
		DisplayName: "go-supervisord",
		Description: "Supervisord service in golang",
		Arguments:   serviceArgs,
	}
	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Error("service init failed", err)
		return err
	}

	action := args[0]
	switch action {
	case "install":
		err := s.Install()
		if err != nil {
			log.Error("Failed to install service go-supervisord: ", err)
			fmt.Println("Failed to install service go-supervisord: ", err)
			return err
		} else {
			fmt.Println("Succeed to install service go-supervisord")
		}
	case "uninstall":
		_ = s.Stop()
		err := s.Uninstall()
		if err != nil {
			log.Error("Failed to uninstall service go-supervisord: ", err)
			fmt.Println("Failed to uninstall service go-supervisord: ", err)
			return err
		} else {
			fmt.Println("Succeed to uninstall service go-supervisord")
		}
	case "start":
		err := s.Start()
		if err != nil {
			log.Error("Failed to start service: ", err)
			fmt.Println("Failed to start service: ", err)
			return err
		} else {
			fmt.Println("Succeed to start service go-supervisord")
		}
	case "stop":
		err := s.Stop()
		if err != nil {
			log.Error("Failed to stop service: ", err)
			fmt.Println("Failed to stop service: ", err)
			return err
		} else {
			fmt.Println("Succeed to stop service go-supervisord")
		}

	default:
		showUsage()
	}

	return nil
}

func (sc *ServiceCommand) RunServer() error {
	if runtime.GOOS != "windows" || service.Interactive() {
		return runForeground()
	}
	serviceArgs := make([]string, 0)
	if options.Configuration != "" {
		serviceArgs = append(serviceArgs, "--configuration="+options.Configuration)
	}
	if options.EnvFile != "" {
		serviceArgs = append(serviceArgs, "--env-file="+options.EnvFile)
	}

	svcConfig := &service.Config{
		Name:        "go-supervisord",
		DisplayName: "go-supervisord",
		Description: "Supervisord service in golang",
		Arguments:   serviceArgs,
	}
	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Error("service init failed", err)
		return err
	}

	return s.Run()
}

func runForeground() error {
	if options.shouldDaemonize() {
		Daemonize(getSupervisordLogFile(options.Configuration), "", func() { _ = serveForeground() })
		return nil
	}
	return serveForeground()
}
func serveForeground() error {
	log.SetOutput(logger.NewReformatLog(os.Stdout))
	s, err := initServer()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			reply := struct{ Ret bool }{}
			_ = s.Shutdown(nil, nil, &reply)
		case <-s.exit:
		}
	}()
	s.WaitForExit()
	return nil
}

func showUsage() {
	fmt.Println("usage: supervisord service install/uninstall/start/stop")
}

func init() {
	_, _ = parser.AddCommand("service",
		"install/uninstall/start/stop service",
		"install/uninstall/start/stop service",
		&serviceCommand)
}
