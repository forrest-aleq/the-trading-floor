package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
	log  *slog.Logger
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

	return db, nil
}

func (db *DB) Close() {
	if db == nil || db.Pool == nil {
		return
	}
	db.Pool.Close()
}

func (db *DB) Migrate(ctx context.Context) error {
	path := os.Getenv("TRADING_FLOOR_MIGRATION_PATH")
	if path == "" {
		path = filepath.Join("store", "migrations", "001_init.sql")
	}

	sqlBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", path, err)
	}

	if _, err := db.Pool.Exec(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("run migration %s: %w", path, err)
	}

	db.log.Info("database migrated", "path", path)
	return nil
}
