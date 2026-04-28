// Tiny CLI around internal/migrator. Usage:
//
//	go run ./cmd/migrate up
//	go run ./cmd/migrate down
//	go run ./cmd/migrate force <version>
//	go run ./cmd/migrate version
//
// DATABASE_URL env var (or -dsn flag) selects the target.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/joho/godotenv"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/migrator"
)

func main() {
	_ = godotenv.Load()

	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "database URL (defaults to $DATABASE_URL)")
	flag.Parse()

	if *dsn == "" {
		fatal("no DSN: set DATABASE_URL or pass -dsn")
	}

	cmd := flag.Arg(0)
	if cmd == "" {
		cmd = "up"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := config.Config{
		DatabaseURL:      *dsn,
		DatabaseMaxConns: 2,
		DatabaseMinConns: 1,
	}
	d, err := db.Open(ctx, cfg)
	if err != nil {
		fatal("db open: %v", err)
	}
	defer func() { _ = d.Close() }()

	m, err := migrator.New(d.Dialect, d.SQL)
	if err != nil {
		fatal("migrator: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			slog.Warn("migrate source close", "err", srcErr)
		}
		if dbErr != nil {
			slog.Warn("migrate db close", "err", dbErr)
		}
	}()

	switch cmd {
	case "up":
		if err := migrator.Up(m); err != nil {
			fatal("up: %v", err)
		}
		fmt.Println("ok")
	case "down":
		if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			fatal("down: %v", err)
		}
		fmt.Println("ok")
	case "force":
		if flag.NArg() < 2 {
			fatal("force requires a version argument")
		}
		v, err := strconv.Atoi(flag.Arg(1))
		if err != nil {
			fatal("force version: %v", err)
		}
		if err := m.Force(v); err != nil {
			fatal("force: %v", err)
		}
		fmt.Printf("forced version %d\n", v)
	case "version":
		v, dirty, err := m.Version()
		if errors.Is(err, migrate.ErrNilVersion) {
			fmt.Println("none")
			return
		}
		if err != nil {
			fatal("version: %v", err)
		}
		fmt.Printf("%d (dirty=%t)\n", v, dirty)
	default:
		fatal("unknown command: %q", cmd)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
