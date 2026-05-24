package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/logging"
	"github.com/K3y5tr0ke/mcpgate/internal/server"
	pgstore "github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	redistore "github.com/K3y5tr0ke/mcpgate/internal/store/redis"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		args = []string{"serve"}
	}

	switch args[0] {
	case "serve":
		return cmdServe(args[1:])
	case "version":
		return cmdVersion()
	case "migrate":
		return cmdMigrate(args[1:])
	case "seed":
		return cmdSeed(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q — use serve, migrate, seed, or version", args[0])
	}
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgFile := fs.String("config", "configs/mcpgate.dev.yaml", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := logging.New(cfg.Logging.Level, cfg.Logging.Format)
	ctx := logging.WithLogger(context.Background(), logger)

	db, err := pgstore.NewPool(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer db.Close()

	redisClient, err := redistore.NewClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer redisClient.Close() //nolint:errcheck

	srv := server.New(cfg, logger, db, redisClient)
	return srv.Run()
}

func cmdVersion() error {
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Version != "" {
		fmt.Printf("mcpgate %s (go %s)\n", info.Main.Version, info.GoVersion)
	} else {
		fmt.Printf("mcpgate (dev build)\n")
	}
	return nil
}

func cmdMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	cfgFile := fs.String("config", "configs/mcpgate.dev.yaml", "path to config file")
	direction := fs.String("direction", "up", "migration direction: up or down")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = cfgFile
	_ = direction
	// Migrations are run via the golang-migrate CLI in the Makefile.
	// This subcommand is a placeholder that prints the equivalent make target.
	fmt.Println("Run database migrations via:")
	fmt.Println("  make migrate-up    # apply all pending migrations")
	fmt.Println("  make migrate-down  # roll back the last migration")
	return nil
}

func cmdSeed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	_ = fs.String("config", "configs/mcpgate.dev.yaml", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Seed logic (default roles, admin user) is implemented in Sprint 1.
	fmt.Println("seed: not yet implemented — coming in Sprint 1")
	return nil
}

func printUsage() {
	fmt.Print(`mcpgate — MCP Proxy Gateway

Usage:
  mcpgate [command] [flags]

Commands:
  serve    Start the proxy server (default)
  migrate  Print migration instructions
  seed     Seed the database with default roles and an admin user
  version  Print version information
  help     Show this help

Flags for serve:
  -config string   Path to config file (default: configs/mcpgate.dev.yaml)
`)
}
