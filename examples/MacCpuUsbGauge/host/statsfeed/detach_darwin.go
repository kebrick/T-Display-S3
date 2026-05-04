//go:build darwin && !ios

package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	trayChildEnvKey = "STATSFEED_TRAY_CHILD"
	trayChildEnvVal = "1"
)

// shSingleQuote — безопасное заключение аргумента в одинарные кавычки для /bin/sh -c
func shSingleQuote(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `'"'"'`) + `'`
}

func maybeDetachTrayDarwin() {
	if os.Getenv(trayChildEnvKey) == trayChildEnvVal {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		log.Printf("tray: executable: %v", err)
		return
	}

	// fork/exec из процесса Go на части машин даёт EPERM (policy / hardened runtime).
	// syscall.Exec заменяет текущий процесс на /bin/sh без fork в Go; уже shell делает
	// nohup … & и exit — дочерний statsfeed форкается внутри sh (другой кодовый путь).
	h, _ := os.UserHomeDir()
	logPath := filepath.Join(h, "Library", "Logs", "statsfeed.log")
	if h == "" {
		logPath = filepath.Join(os.TempDir(), "statsfeed-tray.log")
	}

	var b strings.Builder
	b.WriteString("export ")
	b.WriteString(trayChildEnvKey)
	b.WriteString("=")
	b.WriteString(shSingleQuote(trayChildEnvVal))
	b.WriteString("\nnohup ")
	b.WriteString(shSingleQuote(exe))
	for _, a := range os.Args[1:] {
		b.WriteByte(' ')
		b.WriteString(shSingleQuote(a))
	}
	b.WriteString(" </dev/null >>")
	b.WriteString(shSingleQuote(logPath))
	b.WriteString(" 2>&1 &\nexit 0\n")

	err = syscall.Exec("/bin/sh", []string{"sh", "-c", b.String()}, os.Environ())
	log.Printf("tray: не удалось отсоединиться от терминала (exec /bin/sh): %v", err)
}

func isTrayDetachedChild() bool {
	return os.Getenv(trayChildEnvKey) == trayChildEnvVal
}
