package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

func createUploadsArchive(uploadDir, archivePath string) error {
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := tar.NewWriter(file)
	defer writer.Close()

	return filepath.WalkDir(uploadDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == uploadDir {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to archive symlink in upload directory: %s", path)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to archive non-regular upload entry: %s", path)
		}
		relative, err := filepath.Rel(uploadDir, path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, source)
		closeErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func extractUploadsArchive(archivePath, destinationDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		relative, skip, err := cleanArchivePath(header.Name)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		target := filepath.Join(destinationDir, filepath.FromSlash(relative))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, modeOrDefault(header.FileInfo().Mode().Perm(), 0o750)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			mode := modeOrDefault(header.FileInfo().Mode().Perm(), 0o600)
			destination, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(destination, reader)
			closeErr := destination.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported upload archive entry %q", header.Name)
		}
	}
}

func cleanArchivePath(name string) (string, bool, error) {
	name = filepath.ToSlash(name)
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	cleaned := pathpkg.Clean(name)
	if cleaned == "." || cleaned == "" {
		return "", true, nil
	}
	local := filepath.FromSlash(cleaned)
	if pathpkg.IsAbs(cleaned) || filepath.IsAbs(local) || filepath.VolumeName(local) != "" || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false, fmt.Errorf("unsafe upload archive entry %q", name)
	}
	return cleaned, false, nil
}

func modeOrDefault(mode fs.FileMode, fallback fs.FileMode) fs.FileMode {
	if mode == 0 {
		return fallback
	}
	return mode
}
