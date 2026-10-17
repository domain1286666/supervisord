//go:build !windows && !nacl && !plan9
// +build !windows,!nacl,!plan9

package logger

import (
	"context"
	"errors"
	"fmt"
	"log/syslog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func toSyslogLevel(logLevel string) syslog.Priority {
	switch strings.ToUpper(logLevel) {
	case "EMERG", "LOG_EMERG":
		return syslog.LOG_EMERG
	case "ALERT", "LOG_ALERT":
		return syslog.LOG_ALERT
	case "CRIT", "CRITICAL", "LOG_CRIT", "LOG_CRITICAL":
		return syslog.LOG_CRIT
	case "ERR", "ERROR", "LOG_ERR", "LOG_ERROR":
		return syslog.LOG_ERR
	case "WARNING", "WARN", "LOG_WARNING", "LOG_WARN":
		return syslog.LOG_WARNING
	case "NOTICE", "LOG_NOTICE":
		return syslog.LOG_NOTICE
	case "INFO", "LOG_INFO":
		return syslog.LOG_INFO
	case "DEBUG", "LOG_DEBUG":
		return syslog.LOG_DEBUG
	default:
		return syslog.LOG_INFO
	}
}

func toSyslogFacility(facility string) syslog.Priority {
	switch strings.ToUpper(facility) {
	case "KERN", "KERNEL", "LOG_KERN", "LOG_KERNEL":
		return syslog.LOG_KERN
	case "USER", "LOG_USER":
		return syslog.LOG_USER
	case "MAIL", "LOG_MAIL":
		return syslog.LOG_MAIL
	case "DAEMON", "LOG_DAEMON":
		return syslog.LOG_DAEMON
	case "AUTH", "LOG_AUTH":
		return syslog.LOG_AUTH
	case "SYSLOG", "LOG_SYSLOG":
		return syslog.LOG_SYSLOG
	case "LPR", "LOG_LPR":
		return syslog.LOG_LPR
	case "NEWS", "LOG_NEWS":
		return syslog.LOG_NEWS
	case "UUCP", "LOG_UUCP":
		return syslog.LOG_UUCP
	case "CRON", "LOG_CRON":
		return syslog.LOG_CRON
	case "AUTHPRIV", "LOG_AUTHPRIV":
		return syslog.LOG_AUTHPRIV
	case "FTP", "LOG_FTP":
		return syslog.LOG_FTP
	case "LOCAL0", "LOG_LOCAL0":
		return syslog.LOG_LOCAL0
	case "LOCAL1", "LOG_LOCAL1":
		return syslog.LOG_LOCAL1
	case "LOCAL2", "LOG_LOCAL2":
		return syslog.LOG_LOCAL2
	case "LOCAL3", "LOG_LOCAL3":
		return syslog.LOG_LOCAL3
	case "LOCAL4", "LOG_LOCAL4":
		return syslog.LOG_LOCAL4
	case "LOCAL5", "LOG_LOCAL5":
		return syslog.LOG_LOCAL5
	case "LOCAL6", "LOG_LOCAL6":
		return syslog.LOG_LOCAL6
	case "LOCAL7", "LOG_LOCAL7":
		return syslog.LOG_LOCAL7
	default:
		return syslog.LOG_LOCAL0

	}
}

func getSyslogPriority(props map[string]string) syslog.Priority {
	logLevel := syslog.LOG_NOTICE
	if value, ok := props["syslog_priority"]; ok {
		logLevel = toSyslogLevel(value)
	}
	facility := syslog.LOG_LOCAL0
	if value, ok := props["syslog_facility"]; ok {
		facility = toSyslogFacility(value)
	}
	return logLevel | facility
}

// NewSysLogger creates local syslog
func NewSysLogger(name string, props map[string]string, logEventEmitter LogEventEmitter) *SysLogger {
	priority := getSyslogPriority(props)
	tag := name
	if value, ok := props["syslog_tag"]; ok {
		tag = value
	}
	writer, err := syslog.New(priority, tag)
	logger := &SysLogger{logEventEmitter: logEventEmitter}
	if err == nil {
		logger.logWriter = writer
	}
	return logger
}

// BackendSysLogWriter a syslog writer to write the log to syslog in background
type BackendSysLogWriter struct {
	network    string
	raddr      string
	priority   syslog.Priority
	tag        string
	logChannel chan []byte
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	once       sync.Once
	dropped    atomic.Uint64
}

// NewBackendSysLogWriter creates background syslog writer
func NewBackendSysLogWriter(network, raddr string, priority syslog.Priority, tag string) *BackendSysLogWriter {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackendSysLogWriter{network: network, raddr: raddr, priority: priority, tag: tag, logChannel: make(chan []byte, 128), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	bs.start()
	return bs
}

func (bs *BackendSysLogWriter) start() {
	go func() {
		defer close(bs.done)
		var conn net.Conn
		defer func() {
			if conn != nil {
				_ = conn.Close()
			}
		}()
		backoff := time.Second
		var retryAt time.Time
		host, _ := os.Hostname()
		for {
			select {
			case <-bs.ctx.Done():
				return
			case data := <-bs.logChannel:
				if conn == nil {
					if time.Now().Before(retryAt) {
						bs.dropped.Add(1)
						continue
					}
					var err error
					conn, err = (&net.Dialer{Timeout: 2 * time.Second}).DialContext(bs.ctx, bs.network, bs.raddr)
					if err != nil {
						retryAt = time.Now().Add(backoff)
						backoff = min(backoff*2, 30*time.Second)
						bs.dropped.Add(1)
						continue
					}
					backoff = time.Second
				}
				_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				_, err := fmt.Fprintf(conn, "<%d>%s %s %s[%d]: %s", bs.priority, time.Now().Format(time.Stamp), host, bs.tag, os.Getpid(), data)
				if err != nil {
					_ = conn.Close()
					conn = nil
					retryAt = time.Now().Add(backoff)
					bs.dropped.Add(1)
				}
			}
		}
	}()
}

func (bs *BackendSysLogWriter) Write(data []byte) (int, error) {
	select {
	case <-bs.ctx.Done():
		return 0, os.ErrClosed
	default:
	}
	copy := append([]byte(nil), data...)
	select {
	case <-bs.ctx.Done():
		return 0, os.ErrClosed
	case bs.logChannel <- copy:
	default:
		bs.dropped.Add(1)
	}
	return len(data), nil
}

func (bs *BackendSysLogWriter) Close() error {
	bs.once.Do(bs.cancel)
	<-bs.done
	for {
		select {
		case <-bs.logChannel:
		default:
			return nil
		}
	}
}

func parseSysLogConfig(config string) (protocol string, host string, port int, err error) {
	fields := strings.Split(config, ":")
	host = ""
	protocol = ""
	port = 0
	err = nil
	switch len(fields) {
	case 1:
		host = fields[0]
		port = 514
		protocol = "udp"
	case 2:
		switch fields[0] {
		case "tcp":
			host = fields[1]
			protocol = "tcp"
			port = 6514
		case "udp":
			host = fields[1]
			protocol = "udp"
			port = 514
		default:
			protocol = "udp"
			host = fields[0]
			port, err = strconv.Atoi(fields[1])
			if err != nil {
				return
			}
		}
	case 3:
		protocol = fields[0]
		host = fields[1]
		port, err = strconv.Atoi(fields[2])
		if err != nil {
			return
		}
	default:
		err = errors.New("invalid format")
	}
	return

}

// NewRemoteSysLogger creates network syslog logger object
func NewRemoteSysLogger(name string, config string, props map[string]string, logEventEmitter LogEventEmitter) *SysLogger {
	if len(config) <= 0 {
		return NewSysLogger(name, props, logEventEmitter)
	}

	protocol, host, port, err := parseSysLogConfig(config)
	if err != nil {
		return NewSysLogger(name, props, logEventEmitter)
	}

	priority := getSyslogPriority(props)
	tag := name
	if value, ok := props["syslog_tag"]; ok {
		tag = value
	}

	return &SysLogger{logEventEmitter: logEventEmitter, logWriter: NewBackendSysLogWriter(protocol, net.JoinHostPort(host, strconv.Itoa(port)), priority, tag)}
}
