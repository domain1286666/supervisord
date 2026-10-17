package main

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
)

//go:embed all:webgui
var content embed.FS

// HTTP serves the built console; production requires no external assets.
var HTTP http.FileSystem

func init() {
	if dir := os.Getenv("STATIC_DIR"); dir != "" {
		HTTP = http.Dir(dir)
		return
	}
	files, err := fs.Sub(content, "webgui")
	if err != nil {
		panic(err)
	}
	HTTP = http.FS(files)
}
