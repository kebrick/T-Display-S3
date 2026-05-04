//go:build !darwin || ios

package main

func maybeDetachTrayDarwin() {}

func isTrayDetachedChild() bool { return false }
