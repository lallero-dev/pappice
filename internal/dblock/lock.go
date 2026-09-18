// Package dblock coordinates database use with filesystem replacement.
package dblock

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Mode int

const (
	Shared Mode = iota
	Exclusive
)

var ErrInUse = errors.New("database is in use")

type Lock struct {
	Path string
	file *os.File
}

// Acquire takes a nonblocking lock beside the database. Shared locks permit
// ordinary use; exclusive locks permit replacement. Memory databases need none.
func Acquire(path string, mode Mode) (*Lock, error) {
	path, err := databasePath(path)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return &Lock{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		// A dangling symlink would change lock identity once SQLite creates its target.
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			return nil, err
		}
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
		resolved, err = filepath.Join(parent, filepath.Base(path)), parentErr
	}
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(resolved+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file, mode); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock %s: %w", resolved, err)
	}
	return &Lock{Path: resolved, file: file}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	// Keep the file: removing it could let another process lock a different inode.
	return l.file.Close()
}

func databasePath(path string) (string, error) {
	if strings.HasPrefix(path, "file:") {
		u, err := url.Parse(path)
		if err != nil {
			return "", err
		}
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf("unsupported database URI host %q", u.Host)
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return "", err
		}
		if len(query["mode"]) > 1 {
			return "", errors.New("database URI has multiple mode parameters")
		}
		if query.Get("mode") == "memory" {
			return "", nil
		}
		path = u.Path
		if u.Opaque != "" {
			path, err = url.PathUnescape(u.Opaque)
			if err != nil {
				return "", err
			}
		}
		if strings.HasPrefix(path, "/") && filepath.VolumeName(path[1:]) != "" {
			path = path[1:]
		}
	} else if index := strings.IndexByte(path, '?'); index > 0 {
		path = path[:index]
	}
	if path == "" || path == ":memory:" {
		return "", nil
	}
	return filepath.Abs(filepath.FromSlash(path))
}
