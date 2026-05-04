package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

var instanceFlock *flock.Flock

// ensureSingleRunningInstance — неблокирующая эксклюзивная блокировка; при повторном запуске long-running режима — false.
func ensureSingleRunningInstance() bool {
	base, err := os.UserCacheDir()
	if err != nil {
		log.Printf("statsfeed: кэш-каталог: %v — повторный запуск не отслеживаем", err)
		return true
	}
	path := filepath.Join(base, "statsfeed", "instance.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.Printf("statsfeed: %v", err)
		return true
	}
	instanceFlock = flock.New(path)
	locked, err := instanceFlock.TryLock()
	if err != nil {
		log.Printf("statsfeed: блокировка экземпляра: %v", err)
		return true
	}
	if !locked {
		return false
	}
	return true
}

func releaseInstanceLock() {
	if instanceFlock == nil {
		return
	}
	_ = instanceFlock.Close()
	instanceFlock = nil
}

func exitIfAlreadyRunning() {
	_, _ = fmt.Fprintln(os.Stderr, "statsfeed: уже запущен — выход.")
	os.Exit(0)
}
