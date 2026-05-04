//go:build (!darwin || ios) && !windows

package main

import "log"

func runDesktopBlocking(onQuit func(), rt *feedRuntime) {
	_ = onQuit
	_ = rt
	log.Fatal("internal error: runDesktopBlocking is only used on macOS/Windows")
}
