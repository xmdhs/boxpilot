package core

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func openInstallLock(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// 打开重解析点本身而非目标；禁止删除共享，避免安装器持锁时路径被替换。
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	if err == nil && (info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1) {
		err = errors.New("install lock must be a regular file with one link and no reparse point")
	}
	if err == nil {
		var kind uint32
		kind, err = windows.GetFileType(h)
		if err == nil && kind != windows.FILE_TYPE_DISK {
			err = errors.New("install lock must be a disk file")
		}
	}
	if err == nil {
		err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
