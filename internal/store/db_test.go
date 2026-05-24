package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMigrationFilesDirectorySorted(t *testing.T) {
	dir := t.TempDir()
	mustWriteMigration(t, filepath.Join(dir, "010_tail.sql"))
	mustWriteMigration(t, filepath.Join(dir, "002_init.sql"))
	mustWriteMigration(t, filepath.Join(dir, "020_more.sql"))
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatalf("write non-migration: %v", err)
	}

	files, err := loadMigrationFiles(dir)
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 migrations, got %d", len(files))
	}

	got := []string{files[0].Version, files[1].Version, files[2].Version}
	want := []string{"002_init.sql", "010_tail.sql", "020_more.sql"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected ordered migrations %v, got %v", want, got)
		}
	}
}

func TestLoadMigrationFilesSingleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "001_init.sql")
	mustWriteMigration(t, path)

	files, err := loadMigrationFiles(path)
	if err != nil {
		t.Fatalf("load single migration: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(files))
	}
	if files[0].Version != "001_init.sql" || files[0].Path != path {
		t.Fatalf("unexpected migration metadata: %+v", files[0])
	}
}

func TestLoadMigrationFilesEmptyDirectory(t *testing.T) {
	_, err := loadMigrationFiles(t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty migration directory")
	}
}

func TestPendingExecutionStatusUsesForwardMigration(t *testing.T) {
	hardenPath := filepath.Join("..", "..", "store", "migrations", "002_harden.sql")
	hardenRaw, err := os.ReadFile(hardenPath)
	if err != nil {
		t.Fatalf("read migration %s: %v", hardenPath, err)
	}
	if strings.Contains(string(hardenRaw), "pending_execution") {
		t.Fatalf("expected historical migration %s to remain unchanged", hardenPath)
	}

	pendingPath := filepath.Join("..", "..", "store", "migrations", "009_pending_thesis_status.sql")
	pendingRaw, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatalf("read migration %s: %v", pendingPath, err)
	}
	if !strings.Contains(string(pendingRaw), "pending_execution") {
		t.Fatalf("expected forward migration %s to allow pending_execution", pendingPath)
	}
}

func mustWriteMigration(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("-- migration"), 0o644); err != nil {
		t.Fatalf("write migration %s: %v", path, err)
	}
}
