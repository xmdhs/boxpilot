//go:build !windows

package core

import "errors"

func detectCPUCoreGroups() []CPUCoreGroup { return nil }

func setProcessCPUSetIDs(int, []uint32) error {
	return errors.New("Windows CPU Sets are unavailable on this platform")
}

func RunElevatedCoreHelper([]string) (bool, int, error) { return false, 0, nil }
