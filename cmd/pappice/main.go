package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"pappice/internal/app"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	command, commandArgs := splitCommand(args)
	switch command {
	case "help":
		printRootUsage(stdout)
		return 0
	case "serve":
		return runServe(commandArgs, stderr)
	case "backup":
		return runBackup(commandArgs, stdout, stderr)
	case "restore":
		return runRestore(commandArgs, stdout, stderr)
	case "db":
		return runDB(commandArgs, stdout, stderr)
	case "doctor":
		return runDoctor(commandArgs, stdout, stderr)
	case "healthcheck":
		return runHealthcheck(commandArgs, stdout, stderr)
	case "version":
		return runVersion(commandArgs, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", command)
		printRootUsage(stderr)
		return 2
	}
}

func splitCommand(args []string) (string, []string) {
	if len(args) < 2 {
		return "help", nil
	}
	first := args[1]
	switch first {
	case "-h", "--help", "help":
		return "help", args[2:]
	default:
		return first, args[2:]
	}
}

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, "Pappice customer support ticketing")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  pappice serve [flags]     Start the web server")
	fmt.Fprintln(w, "  pappice backup [flags]    Create a database and uploads backup")
	fmt.Fprintln(w, "  pappice restore [flags]   Restore a backup")
	fmt.Fprintln(w, "  pappice db <command>      Inspect or migrate the SQLite database")
	fmt.Fprintln(w, "  pappice doctor [flags]    Validate local runtime configuration")
	fmt.Fprintln(w, "  pappice healthcheck       Check the local HTTP(S) health endpoint")
	fmt.Fprintln(w, "  pappice version           Print the build version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run \"pappice serve -h\", \"pappice backup -h\", \"pappice restore -h\", \"pappice db -h\", \"pappice doctor -h\", or \"pappice healthcheck -h\" for flags.")
}

func runServe(args []string, stderr io.Writer) int {
	cfg, code, ok := parseRuntimeConfig("pappice serve", args, stderr)
	if !ok {
		return code
	}
	if err := app.Serve(cfg, stderr); err != nil {
		fmt.Fprintf(stderr, "pappice: %v\n", err)
		return 1
	}
	return 0
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pappice version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: pappice version")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "pappice version: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	fmt.Fprintf(stdout, "pappice %s\n", version)
	return 0
}
