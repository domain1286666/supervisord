//go:build windows
// +build windows

package main

func Daemonize(logfile string, pidfile string, proc func()) {
	proc()
}
