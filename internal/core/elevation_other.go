//go:build !windows

package core

func ElevatedCoreSupported() bool { return false }
