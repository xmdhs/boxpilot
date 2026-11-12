//go:build !windows

package core

import "errors"

func detectCPUCoreGroups() []CPUCoreGroup { return nil }

func setProcessCPUAffinity(int, []uint32) error {
	return errors.New("Windows processor affinity is unavailable on this platform")
}

func RunElevatedCoreHelper([]string) (bool, int, error) { return false, 0, nil }
