package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"yuanfa-official/backend/internal/backup"
	"yuanfa-official/backend/internal/coordination"
)

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: yuanfa-backup create --data DIR --output NEW_DIR | verify --backup DIR | restore --backup DIR --data EMPTY_DIR")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	var data, output, source string
	switch args[0] {
	case "create":
		flags.StringVar(&data, "data", "", "stopped DATA_DIR")
		flags.StringVar(&output, "output", "", "new or empty backup directory")
	case "verify":
		flags.StringVar(&source, "backup", "", "sealed backup directory")
	case "restore":
		flags.StringVar(&source, "backup", "", "sealed backup directory")
		flags.StringVar(&data, "data", "", "empty target DATA_DIR")
	default:
		return errors.New("unknown backup command")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	var m *backup.Manifest
	var err error
	switch args[0] {
	case "create":
		if data == "" || output == "" {
			return errors.New("--data and --output required")
		}
		m, err = backup.Create(ctx, data, output)
	case "verify":
		if source == "" {
			return errors.New("--backup required")
		}
		m, err = backup.Verify(ctx, source)
	case "restore":
		if source == "" || data == "" {
			return errors.New("--backup and --data required")
		}
		m, err = backup.Restore(ctx, source, data)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "verified", "command": args[0], "format_version": m.FormatVersion, "schema_version": m.SchemaVersion, "files": len(m.Files)})
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, coordination.ErrDataDirLocked) {
			fmt.Fprintln(os.Stderr, "DATA_DIR_LOCKED")
			os.Exit(73)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
