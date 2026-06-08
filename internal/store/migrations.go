package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type MigrationInfo struct {
	Version int
	Name    string
}

type MigrationStatus struct {
	Path           string
	Empty          bool
	CurrentVersion int
	TargetVersion  int
	Pending        []MigrationInfo
}

type MigrationOptions struct {
	DryRun bool
}

type MigrationResult struct {
	Status      MigrationStatus
	Applied     []MigrationInfo
	DryRun      bool
	Initialized bool
}

type migration struct {
	Version int
	Name    string
	Up      func(*sql.Tx) error
}

var orderedMigrations = []migration{
	{Version: 1, Name: "baseline_schema", Up: migrateBaselineSchema},
}

func CurrentSchemaVersion() int {
	if len(orderedMigrations) == 0 {
		return 0
	}
	return orderedMigrations[len(orderedMigrations)-1].Version
}

func InspectMigration(path string) (MigrationStatus, error) {
	path = defaultDBPath(path)
	status := MigrationStatus{Path: path, TargetVersion: CurrentSchemaVersion()}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			status.Empty = true
			return status, nil
		}
		if err != nil {
			return status, err
		}
		if info.IsDir() {
			return status, fmt.Errorf("%w: database path points to a directory", ErrValidation)
		}
	}

	db, err := openSQLite(path)
	if err != nil {
		return status, err
	}
	defer db.Close()
	if err := configureSQLiteConnection(db); err != nil {
		return status, err
	}
	return inspectMigrationStatus(db, path)
}

func Migrate(path string, opts MigrationOptions) (MigrationResult, error) {
	path = defaultDBPath(path)
	targetPath := path
	cleanup := func() {}
	if opts.DryRun {
		tempPath, err := dryRunDatabase(path)
		if err != nil {
			return MigrationResult{DryRun: true}, err
		}
		targetPath = tempPath
		cleanup = func() { _ = os.Remove(tempPath) }
	}
	defer cleanup()

	db, err := openSQLite(targetPath)
	if err != nil {
		return MigrationResult{DryRun: opts.DryRun}, err
	}
	defer db.Close()
	if err := configureSQLite(db); err != nil {
		return MigrationResult{DryRun: opts.DryRun}, err
	}

	result, err := migrateDB(db, targetPath)
	result.DryRun = opts.DryRun
	result.Status.Path = path
	return result, err
}

func defaultDBPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "pappice.db"
	}
	return path
}

func dryRunDatabase(sourcePath string) (string, error) {
	file, err := os.CreateTemp("", "pappice-migrate-*.db")
	if err != nil {
		return "", err
	}
	tempPath := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := os.Remove(tempPath); err != nil {
		return "", err
	}

	if sourcePath == ":memory:" || strings.HasPrefix(sourcePath, "file:") {
		return "", fmt.Errorf("%w: dry-run requires a filesystem database path", ErrValidation)
	}
	info, err := os.Stat(sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return tempPath, nil
	}
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: database path points to a directory", ErrValidation)
	}

	source, err := openSQLite(sourcePath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	if err := configureSQLiteConnection(source); err != nil {
		return "", err
	}
	if _, err := source.Exec(`VACUUM INTO ?`, tempPath); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("copy database for dry-run: %w", err)
	}
	return tempPath, nil
}

func migrateDB(db *sql.DB, path string) (MigrationResult, error) {
	status, err := inspectMigrationStatus(db, path)
	if err != nil {
		return MigrationResult{Status: status}, err
	}
	result := MigrationResult{Status: status}
	if status.Empty {
		if err := installCurrentSchema(db); err != nil {
			return result, err
		}
		result.Initialized = true
		result.Applied = migrationInfos(orderedMigrations)
		result.Status, err = inspectMigrationStatus(db, path)
		if err != nil {
			return result, err
		}
		return result, validateCurrentSchema(db)
	}
	if status.CurrentVersion > CurrentSchemaVersion() {
		return result, fmt.Errorf("%w: database is at version %d, app supports %d", ErrSchemaTooNew, status.CurrentVersion, CurrentSchemaVersion())
	}
	if len(status.Pending) == 0 {
		return result, validateCurrentSchema(db)
	}
	applied, err := applyPendingMigrations(db, status.Pending)
	result.Applied = applied
	if err != nil {
		return result, err
	}
	result.Status, err = inspectMigrationStatus(db, path)
	if err != nil {
		return result, err
	}
	return result, validateCurrentSchema(db)
}

func inspectMigrationStatus(db *sql.DB, path string) (MigrationStatus, error) {
	status := MigrationStatus{Path: path, TargetVersion: CurrentSchemaVersion()}
	empty, err := databaseIsEmpty(db)
	if err != nil {
		return status, err
	}
	status.Empty = empty
	if empty {
		return status, nil
	}
	hasMigrations, err := tableExists(db, "schema_migrations")
	if err != nil {
		return status, err
	}
	applied := map[int]struct{}{}
	if hasMigrations {
		rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
		if err != nil {
			return status, err
		}
		defer rows.Close()
		for rows.Next() {
			var version int
			if err := rows.Scan(&version); err != nil {
				return status, err
			}
			applied[version] = struct{}{}
			if version > status.CurrentVersion {
				status.CurrentVersion = version
			}
		}
		if err := rows.Err(); err != nil {
			return status, err
		}
	}
	for _, item := range orderedMigrations {
		if _, ok := applied[item.Version]; !ok {
			status.Pending = append(status.Pending, MigrationInfo{Version: item.Version, Name: item.Name})
		}
	}
	return status, nil
}

func installCurrentSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schemaSQL); err != nil {
		return err
	}
	now := nowString()
	for _, item := range orderedMigrations {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			item.Version, item.Name, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func applyPendingMigrations(db *sql.DB, pending []MigrationInfo) ([]MigrationInfo, error) {
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return nil, err
	}
	defer db.Exec(`PRAGMA foreign_keys = ON`)

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return nil, err
	}

	pendingByVersion := map[int]struct{}{}
	for _, item := range pending {
		pendingByVersion[item.Version] = struct{}{}
	}
	var applied []MigrationInfo
	for _, item := range orderedMigrations {
		if _, ok := pendingByVersion[item.Version]; !ok {
			continue
		}
		if err := item.Up(tx); err != nil {
			return applied, fmt.Errorf("migration %03d %s: %w", item.Version, item.Name, err)
		}
		if _, err := tx.Exec(schemaSQL); err != nil {
			return applied, fmt.Errorf("migration %03d %s schema sync: %w", item.Version, item.Name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			item.Version, item.Name, nowString(),
		); err != nil {
			return applied, err
		}
		applied = append(applied, MigrationInfo{Version: item.Version, Name: item.Name})
	}
	if err := tx.Commit(); err != nil {
		return applied, err
	}
	return applied, nil
}

func migrateBaselineSchema(tx *sql.Tx) error {
	hasUsers, err := tableExists(tx, "users")
	if err != nil {
		return err
	}
	if !hasUsers {
		return nil
	}
	hasUsername, err := tableHasColumn(tx, "users", "username")
	if err != nil {
		return err
	}
	if hasUsername {
		return fmt.Errorf("%w: unsupported pre-v0.6 username schema", ErrMigrationRequired)
	}
	return nil
}

type tableQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func databaseIsEmpty(db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type IN ('table', 'index', 'trigger', 'view')
			AND name NOT LIKE 'sqlite_%'`).Scan(&count)
	return count == 0, err
}

func tableExists(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, table string) (bool, error) {
	var count int
	err := q.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
	return count > 0, err
}

func tableHasColumn(q tableQueryer, table, column string) (bool, error) {
	quoted, err := quoteIdentifier(table)
	if err != nil {
		return false, err
	}
	rows, err := q.Query(`PRAGMA table_info(` + quoted + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func quoteIdentifier(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%w: empty identifier", ErrValidation)
	}
	for _, char := range value {
		if char == '_' || char >= '0' && char <= '9' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' {
			continue
		}
		return "", fmt.Errorf("%w: unsafe identifier", ErrValidation)
	}
	return `"` + value + `"`, nil
}

func validateCurrentSchema(db *sql.DB) error {
	hasUsers, err := tableExists(db, "users")
	if err != nil {
		return err
	}
	if !hasUsers {
		return fmt.Errorf("%w: users table is missing", ErrValidation)
	}
	hasUsername, err := tableHasColumn(db, "users", "username")
	if err != nil {
		return err
	}
	if hasUsername {
		return fmt.Errorf("%w: run \"pappice db migrate\"", ErrMigrationRequired)
	}
	var integrity string
	rows, err := db.Query(`PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&integrity); err != nil {
			return err
		}
		if integrity != "ok" {
			return fmt.Errorf("%w: integrity check failed: %s", ErrValidation, integrity)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	fkRows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer fkRows.Close()
	if fkRows.Next() {
		return fmt.Errorf("%w: foreign key check failed", ErrValidation)
	}
	return fkRows.Err()
}

func migrationInfos(items []migration) []MigrationInfo {
	infos := make([]MigrationInfo, 0, len(items))
	for _, item := range items {
		infos = append(infos, MigrationInfo{Version: item.Version, Name: item.Name})
	}
	return infos
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
