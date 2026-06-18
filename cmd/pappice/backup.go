package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	backupops "pappice/internal/backup"
)

func runBackup(args []string, stdout, stderr io.Writer) int {
	cfg, code, ok := parseStorageConfig("pappice backup", args, stderr)
	if !ok {
		return code
	}
	result, err := backupops.Create(backupops.Config{
		DBPath:    cfg.DBPath,
		UploadDir: cfg.UploadDir,
		BackupDir: cfg.BackupDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pappice backup: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Backup created: %s\n", result.Path)
	fmt.Fprintf(stdout, "Database: %s\n", result.DatabasePath)
	if result.HasUploads {
		fmt.Fprintf(stdout, "Uploads: %s\n", result.UploadArchivePath)
	} else {
		fmt.Fprintln(stdout, "Uploads: none")
	}
	fmt.Fprintf(stdout, "Restore with: pappice restore %s\n", result.Path)
	return 0
}

func runRestore(args []string, stdout, stderr io.Writer) int {
	cfg, target, assumeYes, code, ok := parseRestoreConfig(args, stderr)
	if !ok {
		return code
	}
	backupPath, err := backupops.ResolvePath(cfg.BackupDir, target)
	if err != nil {
		fmt.Fprintf(stderr, "pappice restore: %v\n", err)
		return 1
	}
	if !assumeYes && !confirmRestore(stdout, backupPath, cfg) {
		fmt.Fprintln(stderr, "pappice restore: restore cancelled")
		return 1
	}
	result, err := backupops.Restore(backupops.RestoreConfig{
		DBPath:     cfg.DBPath,
		UploadDir:  cfg.UploadDir,
		BackupDir:  cfg.BackupDir,
		BackupPath: backupPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pappice restore: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Restore complete from: %s\n", result.BackupPath)
	fmt.Fprintf(stdout, "Previous files saved in: %s\n", result.SafetyDir)
	return 0
}

func parseStorageConfig(name string, args []string, output io.Writer) (appConfig, int, bool) {
	cfg := defaultAppConfig()
	fs := storageFlagSet(name, &cfg, output)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cfg, 0, false
		}
		return cfg, 2, false
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(output, "%s: unexpected argument %q\n", name, fs.Arg(0))
		return cfg, 2, false
	}
	if err := loadDotEnv(".env"); err != nil {
		fmt.Fprintf(output, "load .env: %v\n", err)
		return cfg, 1, false
	}
	applyStorageEnv(&cfg, fs)
	return cfg, 0, true
}

func parseRestoreConfig(args []string, output io.Writer) (appConfig, string, bool, int, bool) {
	cfg := defaultAppConfig()
	var assumeYes bool
	fs := storageFlagSet("pappice restore", &cfg, output)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: pappice restore [flags] [latest|backup-path]")
		fmt.Fprintln(fs.Output())
		fmt.Fprintln(fs.Output(), "Flags:")
		fs.PrintDefaults()
	}
	fs.BoolVar(&assumeYes, "yes", false, "restore without interactive confirmation")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cfg, "", assumeYes, 0, false
		}
		return cfg, "", assumeYes, 2, false
	}
	if fs.NArg() > 1 {
		fmt.Fprintf(output, "pappice restore: unexpected argument %q\n", fs.Arg(1))
		return cfg, "", assumeYes, 2, false
	}
	if err := loadDotEnv(".env"); err != nil {
		fmt.Fprintf(output, "load .env: %v\n", err)
		return cfg, "", assumeYes, 1, false
	}
	applyStorageEnv(&cfg, fs)
	target := "latest"
	if fs.NArg() == 1 {
		target = fs.Arg(0)
	}
	return cfg, target, assumeYes, 0, true
}

func storageFlagSet(name string, cfg *appConfig, output io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [flags]\n\nFlags:\n", name)
		fs.PrintDefaults()
	}
	fs.StringVar(&cfg.DBPath, "db", cfg.DBPath, "path to SQLite database file")
	fs.StringVar(&cfg.UploadDir, "upload-dir", cfg.UploadDir, "directory for ticket attachment files")
	fs.StringVar(&cfg.BackupDir, "backup-dir", cfg.BackupDir, "directory where backup snapshots are stored")
	return fs
}

func applyStorageEnv(cfg *appConfig, fs *flag.FlagSet) {
	if !flagWasVisited(fs, "db") {
		cfg.DBPath = envOr("PAPPICE_DB", cfg.DBPath)
	}
	if !flagWasVisited(fs, "upload-dir") {
		cfg.UploadDir = envOr("PAPPICE_UPLOAD_DIR", cfg.UploadDir)
	}
	if !flagWasVisited(fs, "backup-dir") {
		cfg.BackupDir = envOr("PAPPICE_BACKUP_DIR", cfg.BackupDir)
	}
}

func confirmRestore(stdout io.Writer, backupPath string, cfg appConfig) bool {
	fmt.Fprintln(stdout, "Stop Pappice before restoring. This will replace:")
	fmt.Fprintf(stdout, "  DB:      %s\n", cfg.DBPath)
	fmt.Fprintf(stdout, "  Uploads: %s\n", cfg.UploadDir)
	fmt.Fprintf(stdout, "Restore from %s? [y/N] ", backupPath)
	var answer string
	if _, err := fmt.Fscan(os.Stdin, &answer); err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
