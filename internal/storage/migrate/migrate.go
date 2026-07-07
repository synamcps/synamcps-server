package migrate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Up applies pending SQL migrations from migrationsPath (default: ./migrations or
// $MIGRATIONS_PATH). No-op when pool is nil.
func Up(pool *pgxpool.Pool, migrationsPath string) error {
	return run(pool, migrationsPath, func(m *migrate.Migrate) error {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	})
}

// Down rolls back the last migration. Intended for development only.
func Down(pool *pgxpool.Pool, migrationsPath string) error {
	return run(pool, migrationsPath, func(m *migrate.Migrate) error {
		if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	})
}

func run(pool *pgxpool.Pool, migrationsPath string, fn func(*migrate.Migrate) error) error {
	if pool == nil {
		return nil
	}
	if migrationsPath == "" {
		migrationsPath = os.Getenv("MIGRATIONS_PATH")
	}
	if migrationsPath == "" {
		migrationsPath = "migrations"
	}
	abs, err := filepath.Abs(migrationsPath)
	if err != nil {
		return fmt.Errorf("resolve migrations path: %w", err)
	}
	dirFS := os.DirFS(abs)
	sub, err := fs.Sub(dirFS, ".")
	if err != nil {
		return fmt.Errorf("open migrations dir: %w", err)
	}
	sourceDriver, err := iofs.New(sub, ".")
	if err != nil {
		return fmt.Errorf("migrations source: %w", err)
	}

	db := stdlib.OpenDBFromPool(pool)

	dbDriver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("migration db driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "postgres", dbDriver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()

	if err := fn(m); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
