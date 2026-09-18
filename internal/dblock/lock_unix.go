//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package dblock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(file *os.File, mode Mode) error {
	flags := unix.LOCK_SH | unix.LOCK_NB
	if mode == Exclusive {
		flags = unix.LOCK_EX | unix.LOCK_NB
	}
	err := unix.Flock(int(file.Fd()), flags)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrInUse
	}
	return err
}
