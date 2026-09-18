package dblock

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabasePathAliasesShareLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a database.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(path, Shared)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=rw"}
	aliases := []string{relative, u.String(), path + "?_pragma=busy_timeout(1000)"}
	link := filepath.Join(filepath.Dir(path), "alias.db")
	if err := os.Symlink(path, link); err == nil {
		aliases = append(aliases, link)
	}
	parentLink := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(path), parentLink); err == nil {
		aliases = append(aliases, filepath.Join(parentLink, filepath.Base(path)))
	}
	for _, alias := range aliases {
		if other, err := Acquire(alias, Exclusive); !errors.Is(err, ErrInUse) {
			_ = other.Close()
			t.Fatalf("alias %q bypasses lock: %v", alias, err)
		}
	}
}

func TestDanglingDatabaseSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "alias.db")
	if err := os.Symlink(filepath.Join(dir, "missing.db"), link); err != nil {
		t.Skip(err)
	}
	lock, err := Acquire(link, Shared)
	defer lock.Close()
	if err == nil {
		t.Fatal("dangling database symlink acquired an unstable lock")
	}
}

func TestMemoryDatabasesNeedNoLock(t *testing.T) {
	for _, path := range []string{":memory:", "file::memory:", "file:private?mode=memory&cache=shared"} {
		lock, err := Acquire(path, Shared)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		if lock.Path != "" || lock.file != nil {
			t.Fatalf("memory database acquired a file lock: %q", path)
		}
	}
}
