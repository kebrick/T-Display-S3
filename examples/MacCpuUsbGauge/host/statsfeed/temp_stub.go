//go:build !windows

package main

func readWindowsAuxiliaryCPUTemp() float64 { return -1 }
