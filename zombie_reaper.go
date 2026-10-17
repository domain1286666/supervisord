//go:build !windows && !linux

package main

// ReapZombie leaves orphan adoption to the native init; exec.Cmd reaps owned children.
func ReapZombie() {}
