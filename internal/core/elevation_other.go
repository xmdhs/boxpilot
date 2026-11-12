//go:build !windows

package core

func ElevatedCoreSupported() bool { return false }

func RunElevatedCoreAction([]string) (bool, int, error) { return false, 0, nil }
