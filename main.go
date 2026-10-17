package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/hashicorp/go-envparse"
	"github.com/jessevdk/go-flags"
	"github.com/ochinchina/go-ini"
	"github.com/ochinchina/supervisord/config"
	"github.com/ochinchina/supervisord/logger"
	log "github.com/sirupsen/logrus"
)

var BuildVersion string = ""

// Options the command line options
type Options struct {
	Configuration string `short:"c" long:"configuration" description:"the configuration file"`
	Daemon        bool   `short:"d" long:"daemon" description:"run as daemon"`
	EnvFile       string `long:"env-file" description:"the environment file"`
	// NoDaemon and LogLevel accept Python supervisord's -n and -e so that
	// command lines written for it keep working.
	NoDaemon bool   `short:"n" long:"nodaemon" description:"run in the foreground, the default; overrides -d"`
	LogLevel string `short:"e" long:"loglevel" description:"log level (critical, error, warn, info, debug); overrides loglevel in the configuration file"`
}

// shouldDaemonize reports whether supervisord should detach; -n wins over -d
// as it does in Python supervisord.
func (o *Options) shouldDaemonize() bool {
	return o.Daemon && !o.NoDaemon
}

func init() {
	nullLogger := logger.NewNullLogger(logger.NewNullLogEventEmitter())
	//log.SetOutput(nullLogger)
	log.SetOutput(logger.NewReformatLog(nullLogger))
	logFormat := os.Getenv("LOG_FORMAT")

	if logFormat == "json" {
		log.SetFormatter(&log.JSONFormatter{})
	} else {
		if runtime.GOOS == "windows" {
			log.SetFormatter(&log.TextFormatter{DisableColors: true, FullTimestamp: true})
		} else {
			log.SetFormatter(&log.TextFormatter{DisableColors: false, FullTimestamp: true})
		}
	}
	log.SetLevel(log.DebugLevel)
}

var options Options
var parser = flags.NewParser(&options, flags.Default & ^flags.PrintErrors)

func loadEnvironmentFile(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open environment file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("read environment file: %w", err)
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("environment file exceeds 1 MiB")
	}
	values, err := envparse.Parse(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid environment file syntax")
	}
	for name, value := range values {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set environment variable %s: %w", name, err)
		}
	}
	return nil
}

// find the supervisord.conf in following order:
//
// 1. $CWD/supervisord.conf
// 2. $CWD/etc/supervisord.conf
// 3. /etc/supervisord.conf
// 4. /etc/supervisor/supervisord.conf (since Supervisor 3.3.0)
// 5. ../etc/supervisord.conf (Relative to the executable)
// 6. ../supervisord.conf (Relative to the executable)
func findSupervisordConf() (string, error) {
	possibleSupervisordConf := []string{options.Configuration,
		"./supervisord.ini",
		"./etc/supervisord.conf",
		"/etc/supervisord.conf",
		"/etc/supervisor/supervisord.conf",
		"../etc/supervisord.conf",
		"../supervisord.conf",
		"./supervisord.conf"}

	for _, file := range possibleSupervisordConf {
		if _, err := os.Stat(file); err == nil {
			absFile, err := filepath.Abs(file)
			if err == nil {
				return absFile, nil
			}
			return file, nil
		}
	}

	return "", fmt.Errorf("fail to find supervisord.conf")
}

func initServer() (*Supervisor, error) {
	if err := loadEnvironmentFile(options.EnvFile); err != nil {
		return nil, err
	}
	if len(options.Configuration) <= 0 {
		options.Configuration, _ = findSupervisordConf()
	}
	s := NewSupervisor(options.Configuration)
	if sErr := s.Reload(RestartAll); sErr != nil {
		s.xmlRPC.Stop()
		s.procMgr.Close()
		return nil, sErr
	}
	return s, nil
}

// Get the supervisord log file
func getSupervisordLogFile(configFile string) string {
	configFileDir := filepath.Dir(configFile)
	env := config.NewStringExpression("here", configFileDir)
	myini := ini.NewIni()
	myini.LoadFile(configFile)
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	logFile := myini.GetValueWithDefault("supervisord", "logfile", filepath.Join(cwd, "supervisord.log"))
	logFile, err = env.Eval(logFile)
	if err == nil {
		return logFile
	} else {
		return filepath.Join(".", "supervisord.log")
	}
}

func main() {
	if BuildVersion != "" {
		version = BuildVersion
	}

	ReapZombie()

	// when execute `supervisord` without sub-command, it should start the server
	parser.Command.SubcommandsOptional = true
	parser.CommandHandler = func(command flags.Commander, args []string) error {
		if command == nil {
			pureService := &ServiceCommand{}
			return pureService.RunServer()
		}
		return command.Execute(args)
	}

	if _, err := parser.Parse(); err != nil {
		flagsErr, ok := err.(*flags.Error)
		if ok {
			switch flagsErr.Type {
			case flags.ErrHelp:
				_, _ = fmt.Fprintln(os.Stdout, err)
				os.Exit(0)
			default:
				_, _ = fmt.Fprintf(os.Stderr, "error when parsing command: %s\n", err)
				os.Exit(1)
			}
		} else {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

}
