//go:build !windows

package core

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openInstallLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	err = unix.Fstat(fd, &stat)
	if err == nil && (stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1) {
		err = errors.New("install lock must be a regular file with one link")
	}
	if err == nil {
		for {
			err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
			if !errors.Is(err, unix.EINTR) {
				break
			}
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
