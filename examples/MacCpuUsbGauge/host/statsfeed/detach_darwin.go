//go:build darwin && !ios

package main

import (
	"log"
	"os"
	"os/exec"
	"syscall"
)

const (
	trayChildEnvKey = "STATSFEED_TRAY_CHILD"
	trayChildEnvVal = "1"
)

func maybeDetachTrayDarwin() {
	if os.Getenv(trayChildEnvKey) == trayChildEnvVal {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer devNull.Close()

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), trayChildEnvKey+"="+trayChildEnvVal)
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Printf("tray: не удалось отсоединиться от терминала: %v", err)
		return
	}
	os.Exit(0)
}

func isTrayDetachedChild() bool {
	return os.Getenv(trayChildEnvKey) == trayChildEnvVal
}
