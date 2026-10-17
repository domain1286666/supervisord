package process

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"io"
	"net"
	"net/http"
	"os"

	"strings"
	"sync"

	"time"
)

// FileExists returns true if the file exists at the given path.
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	return err == nil
}

type ScriptExecutor struct {
	script         string
	executeTimeout uint32
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	transport      *http.Transport
}

func NewScriptExecutorWithTimeout(script string, timeout uint32) *ScriptExecutor {
	return NewScriptExecutorWithContext(context.Background(), script, timeout)
}

// NewScriptExecutorWithContext creates a bounded executor owned by its parent.
func NewScriptExecutorWithContext(parent context.Context, script string, timeout uint32) *ScriptExecutor {
	ctx, cancel := context.WithCancel(parent)
	return &ScriptExecutor{script: script, executeTimeout: min(3600, max(1, timeout)), ctx: ctx, cancel: cancel}
}

func NewScriptExecutor(script string) *ScriptExecutor {
	return NewScriptExecutorWithTimeout(script, 60)
}

func (se *ScriptExecutor) Execute() error {
	if strings.HasPrefix(se.script, "http://") || strings.HasPrefix(se.script, "https://") {
		return se.executeHTTP()
	} else if strings.HasPrefix(se.script, "tcp://") {
		return se.executeTCP()
	} else {
		return se.executeLocal()
	}
}

// Execute the script in remote machine via HTTP request
// @return error if the script execution failed
func (se *ScriptExecutor) executeHTTP() error {

	fields, err := parseCommand(se.script)
	if err != nil {
		return err
	}

	url := fields[0]
	if len(fields) > 1 {
		options, _, headers := se.parseHttpParameters(fields[1:])
		key_file, cert_file, ca_file := options["key-file"], options["cert-file"], options["ca-file"]
		data := se.loadData(options)

		return se.executeHttpRequest(url, key_file, cert_file, ca_file, data, headers)
	} else {
		return se.executeHttpRequest(url, "", "", "", make([]byte, 0), make(map[string]string))
	}

}

// loadData load the data from options["data"] or options["d"]
// If the data starts with "@", it will be treated as a file path and the content of the file will be loaded as data.
// @return the data as []byte, or nil if no data is provided or the file does not exist
func (se *ScriptExecutor) loadData(options map[string]string) []byte {
	data := options["data"]
	if data == "" {
		data = options["d"]
	}
	if data == "" {
		return make([]byte, 0)
	}
	if strings.HasPrefix(data, "@") {
		dataFile := strings.TrimPrefix(data, "@")
		if fileExists(dataFile) {
			content, err := readLimitedFile(dataFile, 1<<20)
			if err == nil {
				return content
			} else {
				return make([]byte, 0)
			}
		} else {
			return make([]byte, 0)
		}
	} else {
		return []byte(data)
	}
}

// Execute the script in remote machine via HTTP request
// @return error if the script execution failed
func (se *ScriptExecutor) executeHttpRequest(url, keyFile, certFile, caFile string, data []byte, headers map[string]string) error {
	se.mu.Lock()
	if se.transport == nil {
		tlsConfig, err := se.createTlsConfig(keyFile, certFile, caFile)
		if err != nil {
			se.mu.Unlock()
			return err
		}
		se.transport = http.DefaultTransport.(*http.Transport).Clone()
		se.transport.TLSClientConfig = tlsConfig
		se.transport.MaxIdleConnsPerHost = 2
		se.transport.IdleConnTimeout = 30 * time.Second
	}
	transport := se.transport
	se.mu.Unlock()
	ctx, cancel := context.WithTimeout(se.ctx, time.Duration(se.executeTimeout)*time.Second)
	defer cancel()
	method := "GET"
	if len(data) > 0 {
		method = "POST"
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Transport: transport, Timeout: time.Duration(se.executeTimeout) * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return err
	}
	if n > 1<<20 {
		return errors.New("health response exceeds 1 MiB")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP health check status %d", resp.StatusCode)
	}
	return nil
}

func (se *ScriptExecutor) createTlsConfig(keyFile, certFile, caFile string) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid CA certificate")
		}
		config.RootCAs = pool
	}
	if keyFile != "" || certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}
		config.Certificates = []tls.Certificate{cert}
	}
	return config, nil
}

func (se *ScriptExecutor) executeLocal() error {
	args, err := parseCommand(strings.TrimPrefix(se.script, "script://"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(se.ctx, time.Duration(se.executeTimeout)*time.Second)
	defer cancel()
	cmd := auxiliaryCommand(ctx, args)
	cmd.WaitDelay = time.Second
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return RunCommand(cmd)
}

func (se *ScriptExecutor) executeTCP() error {
	ctx, cancel := context.WithTimeout(se.ctx, time.Duration(se.executeTimeout)*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(se.script, "tcp://"))
	if err != nil {
		return err
	}
	return conn.Close()
}

// Close cancels work and releases the executor's idle HTTP connections.
func (se *ScriptExecutor) Close() {
	se.cancel()
	se.mu.Lock()
	defer se.mu.Unlock()
	if se.transport != nil {
		se.transport.CloseIdleConnections()
	}
}

func (se *ScriptExecutor) parseHttpParameters(params []string) (map[string]string, []string, map[string]string) {
	index := 0
	options := make(map[string]string)
	paramsList := make([]string, 0)
	headers := make(map[string]string)
	for index < len(params) {
		// parse options in the form of -key value or --key value
		if strings.HasPrefix(params[index], "-") && index+1 < len(params) {
			if params[index] == "-H" || params[index] == "--header" {
				header := params[index+1]
				kv := strings.SplitN(header, ":", 2)
				if len(kv) == 2 {
					headers[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
				}
			} else {
				name := ""
				if strings.HasPrefix(params[index], "--") {
					name = params[index][2:]
				} else {
					name = params[index][1:]
				}
				options[name] = params[index+1]
			}
			index += 2
		} else { // parse parameters in the form of value
			paramsList = append(paramsList, params[index])
			index++
		}
	}

	return options, paramsList, headers

}
