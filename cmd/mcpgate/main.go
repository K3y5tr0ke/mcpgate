package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/logging"
	"github.com/K3y5tr0ke/mcpgate/internal/server"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
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

	srv, err := server.New(cfg, logger, db, redisClient)
	if err != nil {
		return fmt.Errorf("init server: %w", err)
	}
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

	userStore := pgstore.NewUserStore(db)
	roleStore := pgstore.NewRoleStore(db)

	const adminEmail = "admin@localhost"

	password, err := generatePassword(20)
	if err != nil {
		return fmt.Errorf("generate password: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	user, err := userStore.GetByEmail(ctx, adminEmail)
	if errors.Is(err, store.ErrNotFound) {
		user = &store.User{
			ID:          uuid.New(),
			Email:       adminEmail,
			DisplayName: "Local Admin",
			IDPSubject:  "local-admin",
			IDPName:     "local",
			Status:      "active",
			Metadata:    map[string]any{},
		}
		if err := userStore.Create(ctx, user); err != nil {
			return fmt.Errorf("create admin user: %w", err)
		}
		fmt.Println("Created local admin user:", adminEmail)
	} else if err != nil {
		return fmt.Errorf("lookup admin user: %w", err)
	} else {
		fmt.Println("Resetting password for existing local admin:", adminEmail)
	}

	if err := userStore.SetPasswordHash(ctx, user.ID, string(hash)); err != nil {
		return fmt.Errorf("set password hash: %w", err)
	}

	adminRole, err := roleStore.GetByName(ctx, "admin")
	if err != nil {
		return fmt.Errorf("lookup admin role (run make migrate-up first): %w", err)
	}
	if err := roleStore.AssignToUser(ctx, user.ID, adminRole.ID, user.ID); err != nil {
		return fmt.Errorf("assign admin role: %w", err)
	}

	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  Local admin credentials")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Email:    %s\n", adminEmail)
	fmt.Printf("  Password: %s\n", password)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("  Store this password — it is not recoverable.")
	fmt.Println("  Run 'make seed' again to reset it.")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return nil
}

// generatePassword returns a random alphanumeric string of the given length.
func generatePassword(length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i, v := range b {
		b[i] = chars[int(v)%len(chars)]
	}
	return string(b), nil
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
