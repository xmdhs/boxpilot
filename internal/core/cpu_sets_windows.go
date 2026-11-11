//go:build windows

package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	getSystemCPUSetInformation = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemCpuSetInformation")
	setProcessDefaultCPUSets   = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessDefaultCpuSets")
)

func detectCPUCoreGroups() []CPUCoreGroup {
	if err := getSystemCPUSetInformation.Find(); err != nil {
		return nil
	}
	process, err := windows.GetCurrentProcess()
	if err != nil {
		return nil
	}
	var required uint32
	_, _, _ = getSystemCPUSetInformation.Call(0, 0, uintptr(unsafe.Pointer(&required)), uintptr(process), 0)
	if required < 24 || required > 16<<20 {
		return nil
	}
	buffer := make([]byte, required)
	var returned uint32
	ok, _, _ := getSystemCPUSetInformation.Call(
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&returned)),
		uintptr(process),
		0,
	)
	runtime.KeepAlive(buffer)
	if ok == 0 || returned > uint32(len(buffer)) {
		return nil
	}
	buffer = buffer[:returned]

	sets := make([]cpuSetInfo, 0, len(buffer)/32)
	for offset := 0; offset+24 <= len(buffer); {
		size := int(binary.LittleEndian.Uint32(buffer[offset : offset+4]))
		kind := binary.LittleEndian.Uint32(buffer[offset+4 : offset+8])
		if size < 24 || size > len(buffer)-offset {
			return nil
		}
		// CpuSetInformation is enum value zero. The union begins at byte 8.
		if kind == 0 {
			sets = append(sets, cpuSetInfo{
				id:              binary.LittleEndian.Uint32(buffer[offset+8 : offset+12]),
				efficiencyClass: buffer[offset+18],
				schedulingClass: buffer[offset+20],
			})
		}
		offset += size
	}
	return classifyCPUCoreGroups(sets)
}

func setProcessCPUSetIDs(pid int, ids []uint32) error {
	if pid <= 0 {
		return errors.New("invalid sing-box process ID")
	}
	if err := setProcessDefaultCPUSets.Find(); err != nil {
		return fmt.Errorf("Windows CPU Sets are unavailable: %w", err)
	}
	handle, err := windows.OpenProcess(0x2000 /* PROCESS_SET_LIMITED_INFORMATION */, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open managed sing-box process: %w", err)
	}
	defer windows.CloseHandle(handle)

	var idsPointer uintptr
	if len(ids) > 0 {
		idsPointer = uintptr(unsafe.Pointer(&ids[0]))
	}
	ok, _, callErr := setProcessDefaultCPUSets.Call(uintptr(handle), idsPointer, uintptr(len(ids)))
	runtime.KeepAlive(ids)
	if ok == 0 {
		if callErr == windows.ERROR_SUCCESS {
			callErr = errors.New("SetProcessDefaultCpuSets failed")
		}
		return callErr
	}
	return nil
}
