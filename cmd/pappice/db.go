package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"pappice/internal/store"
)

func runDB(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printDBUsage(stdout)
		return 0
	}
	command, commandArgs := args[0], args[1:]
	switch command {
	case "status":
		return runDBStatus(commandArgs, stdout, stderr)
	case "migrate":
		return runDBMigrate(commandArgs, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "pappice db: unknown command %q\n\n", command)
		printDBUsage(stderr)
		return 2
	}
}

func printDBUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  pappice db status [flags]          Show schema version and pending migrations")
	fmt.Fprintln(w, "  pappice db migrate [flags]         Apply pending database migrations")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run \"pappice db status -h\" or \"pappice db migrate -h\" for flags.")
}

func runDBStatus(args []string, stdout, stderr io.Writer) int {
	cfg, code, ok := parseDBConfig("pappice db status", args, stderr)
	if !ok {
		return code
	}
	status, err := store.InspectMigration(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(stderr, "pappice db status: %v\n", err)
		return 1
	}
	printMigrationStatus(stdout, status)
	if status.CurrentVersion > status.TargetVersion {
		return 1
	}
	return 0
}

func runDBMigrate(args []string, stdout, stderr io.Writer) int {
	cfg, dryRun, code, ok := parseDBMigrateConfig(args, stderr)
	if !ok {
		return code
	}
	result, err := store.Migrate(cfg.DBPath, store.MigrationOptions{DryRun: dryRun})
	if err != nil {
		fmt.Fprintf(stderr, "pappice db migrate: %v\n", err)
		return 1
	}
	printMigrationResult(stdout, result)
	return 0
}

func parseDBConfig(name string, args []string, output io.Writer) (appConfig, int, bool) {
	cfg := defaultAppConfig()
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [flags]\n\nFlags:\n", name)
		fs.PrintDefaults()
	}
	fs.StringVar(&cfg.DBPath, "db", cfg.DBPath, "path to SQLite database file")
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
	if !flagWasVisited(fs, "db") {
		cfg.DBPath = envOr("PAPPICE_DB", cfg.DBPath)
	}
	return cfg, 0, true
}

func parseDBMigrateConfig(args []string, output io.Writer) (appConfig, bool, int, bool) {
	cfg := defaultAppConfig()
	var dryRun bool
	fs := flag.NewFlagSet("pappice db migrate", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: pappice db migrate [flags]")
		fmt.Fprintln(fs.Output())
		fmt.Fprintln(fs.Output(), "Flags:")
		fs.PrintDefaults()
	}
	fs.StringVar(&cfg.DBPath, "db", cfg.DBPath, "path to SQLite database file")
	fs.BoolVar(&dryRun, "dry-run", false, "validate migrations on a temporary database copy")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cfg, dryRun, 0, false
		}
		return cfg, dryRun, 2, false
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(output, "pappice db migrate: unexpected argument %q\n", fs.Arg(0))
		return cfg, dryRun, 2, false
	}
	if err := loadDotEnv(".env"); err != nil {
		fmt.Fprintf(output, "load .env: %v\n", err)
		return cfg, dryRun, 1, false
	}
	if !flagWasVisited(fs, "db") {
		cfg.DBPath = envOr("PAPPICE_DB", cfg.DBPath)
	}
	return cfg, dryRun, 0, true
}

func flagWasVisited(fs *flag.FlagSet, name string) bool {
	visited := false
	fs.Visit(func(item *flag.Flag) {
		if item.Name == name {
			visited = true
		}
	})
	return visited
}

func printMigrationStatus(w io.Writer, status store.MigrationStatus) {
	fmt.Fprintln(w, "Pappice database")
	fmt.Fprintf(w, "Path: %s\n", status.Path)
	fmt.Fprintf(w, "Schema: %d / %d\n", status.CurrentVersion, status.TargetVersion)
	switch {
	case status.Empty:
		fmt.Fprintln(w, "Status: empty; current schema will be installed on first start or migrate")
	case status.CurrentVersion > status.TargetVersion:
		fmt.Fprintln(w, "Status: too new for this Pappice binary")
	case len(status.Pending) == 0:
		fmt.Fprintln(w, "Status: current")
	default:
		fmt.Fprintln(w, "Status: migration required")
		printMigrationList(w, "Pending", status.Pending)
	}
}

func printMigrationResult(w io.Writer, result store.MigrationResult) {
	if result.DryRun {
		fmt.Fprintln(w, "Dry-run succeeded. Database was not changed.")
	} else {
		fmt.Fprintln(w, "Migration succeeded.")
	}
	if result.Initialized {
		fmt.Fprintln(w, "Initialized current schema.")
	}
	if len(result.Applied) == 0 {
		fmt.Fprintln(w, "Applied: none")
		return
	}
	printMigrationList(w, "Applied", result.Applied)
}

func printMigrationList(w io.Writer, label string, migrations []store.MigrationInfo) {
	fmt.Fprintf(w, "%s:\n", label)
	for _, item := range migrations {
		fmt.Fprintf(w, "  %03d %s\n", item.Version, strings.ReplaceAll(item.Name, "_", "-"))
	}
}
