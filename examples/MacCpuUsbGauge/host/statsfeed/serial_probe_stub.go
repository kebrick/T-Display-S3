//go:build !windows

package main

import serial "go.bug.st/serial"

func serialPortProbeHWDead(p serial.Port) bool {
	_ = p
	return false
}
