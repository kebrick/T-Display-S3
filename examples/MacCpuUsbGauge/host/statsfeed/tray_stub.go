//go:build (!darwin || ios) && !windows

package main

import "log"

func runTrayBlocking(onQuit func()) {
	_ = onQuit
	log.Fatal("internal error: runTrayBlocking is only used on macOS/Windows")
}
