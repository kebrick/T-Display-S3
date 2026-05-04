//go:build (darwin && !ios) || windows

package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

func setupTrayLogForGUI() {
	path := trayLogFilePath()
	if path == "" {
		log.SetOutput(io.Discard)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.SetOutput(io.Discard)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.SetOutput(io.Discard)
		return
	}
	log.SetOutput(f)
}

func trayLogFilePath() string {
	switch runtime.GOOS {
	case "darwin":
		h, err := os.UserHomeDir()
		if err != nil || h == "" {
			return ""
		}
		return filepath.Join(h, "Library", "Logs", "statsfeed.log")
	case "windows":
		la := os.Getenv("LOCALAPPDATA")
		if la == "" {
			return ""
		}
		return filepath.Join(la, "statsfeed", "statsfeed.log")
	default:
		return ""
	}
}
