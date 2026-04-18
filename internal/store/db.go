package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool                    *pgxpool.Pool
	log                     *slog.Logger
	signalEmbeddingIsVector bool
}

type migrationFile struct {
	Version string
	Path    string
}

func NewDB(ctx context.Context) (*DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost:5432/tradingfloor?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	db := &DB{
		Pool: pool,
		log:  slog.Default().With("component", "store"),
	}

	if err := db.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	if err := db.detectCapabilities(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return db, nil
}

func (db *DB) Close() {
	if db == nil || db.Pool == nil {
		return
	}
	db.Pool.Close()
}

func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.Pool == nil {
		return fmt.Errorf("postgres pool unavailable")
	}
	return db.Pool.Ping(ctx)
}

func (db *DB) Migrate(ctx context.Context) error {
	path := migrationPath()
	migrations, err := loadMigrationFiles(path)
	if err != nil {
		return err
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     TEXT PRIMARY KEY,
			applied_at  TIMESTAMPTZ DEFAULT NOW() NOT NULL
		)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	applied, err := db.appliedMigrations(ctx)
	if err != nil {
		return err
	}

	for _, migration := range migrations {
		if _, ok := applied[migration.Version]; ok {
			continue
		}

		sqlBytes, err := os.ReadFile(migration.Path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", migration.Path, err)
		}

		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", migration.Version, err)
		}

		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("run migration %s: %w", migration.Path, err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT (version) DO NOTHING`,
			migration.Version,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", migration.Version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.Version, err)
		}

		db.log.Info("database migrated", "version", migration.Version, "path", migration.Path)
	}

	return nil
}

func (db *DB) appliedMigrations(ctx context.Context) (map[string]struct{}, error) {
	rows, err := db.Pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]struct{})
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return applied, nil
}

func migrationPath() string {
	path := os.Getenv("TRADING_FLOOR_MIGRATION_PATH")
	if path == "" {
		return filepath.Join("store", "migrations")
	}
	return path
}

func loadMigrationFiles(path string) ([]migrationFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat migration path %s: %w", path, err)
	}

	if !info.IsDir() {
		if filepath.Ext(path) != ".sql" {
			return nil, fmt.Errorf("migration path %s is not a .sql file", path)
		}
		return []migrationFile{{
			Version: filepath.Base(path),
			Path:    path,
		}}, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read migration dir %s: %w", path, err)
	}

	migrations := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		migrations = append(migrations, migrationFile{
			Version: entry.Name(),
			Path:    filepath.Join(path, entry.Name()),
		})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no .sql migrations found in %s", path)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return strings.Compare(migrations[i].Version, migrations[j].Version) < 0
	})
	return migrations, nil
}

func (db *DB) detectCapabilities(ctx context.Context) error {
	var udtName string
	err := db.Pool.QueryRow(ctx, `
		SELECT COALESCE(udt_name, '')
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'signals'
		  AND column_name = 'embedding'
	`).Scan(&udtName)
	if err != nil {
		return fmt.Errorf("detect signals.embedding type: %w", err)
	}

	db.signalEmbeddingIsVector = udtName == "vector"
	db.log.Info("store capabilities detected", "signals_embedding_vector", db.signalEmbeddingIsVector)
	return nil
}
