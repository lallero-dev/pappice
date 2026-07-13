package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
)

type sqliteBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

func copySQLiteDatabase(sourcePath, destinationPath string) error {
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o750); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		return err
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(PASSIVE);`); err != nil {
		return fmt.Errorf("failed to checkpoint sqlite: %w", err)
	}

	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(sqliteBackuper)
		if !ok {
			return errors.New("sqlite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(destinationPath)
		if err != nil {
			return err
		}
		for more := true; more; {
			more, err = backup.Step(-1)
			if err != nil {
				if err := backup.Finish(); err != nil {
					return fmt.Errorf("failed to finalize sqlite backup: %w", err)
				}
				return err
			}
		}
		if err := backup.Finish(); err != nil {
			return fmt.Errorf("failed to finalize sqlite backup: %w", err)
		}
		return nil
	})
}
