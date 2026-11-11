package core

import (
	"fmt"
	"os"
)

// acquireInstallLock 兼容旧标记文件，但不截断或删除它们。
// 返回的文件持有非阻塞系统锁，直到关闭句柄或进程退出。
// 调用方必须保证持锁期间私有父目录不被替换。
func acquireInstallLock(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("unsafe install lock: %s", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	f, err := openInstallLock(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		var entry os.FileInfo
		entry, err = os.Lstat(path)
		if err == nil && (!info.Mode().IsRegular() || !entry.Mode().IsRegular() || !os.SameFile(info, entry)) {
			err = fmt.Errorf("unsafe or replaced install lock: %s", path)
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
