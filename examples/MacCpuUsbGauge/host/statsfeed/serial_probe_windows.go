//go:build windows

package main

import serial "go.bug.st/serial"

// serialPortProbeHWDead: запасной признак «устройство отвалилось», когда Read ещё даёт только таймаут.
func serialPortProbeHWDead(p serial.Port) bool {
	_, err := p.GetModemStatusBits()
	return err != nil
}
