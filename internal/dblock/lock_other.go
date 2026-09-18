//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package dblock

import (
	"errors"
	"os"
)

func lockFile(*os.File, Mode) error {
	return errors.New("filesystem database locking is unavailable on this platform")
}
