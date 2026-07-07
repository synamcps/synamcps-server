package migrate_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationFilesPresent(t *testing.T) {
	root := filepath.Join("..", "..", "..", "migrations")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var ups, downs int
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".sql" {
			continue
		}
		if len(name) > 7 && name[len(name)-7:] == ".up.sql" {
			ups++
		}
		if len(name) > 9 && name[len(name)-9:] == ".down.sql" {
			downs++
		}
	}
	if ups == 0 {
		t.Fatal("expected at least one .up.sql migration")
	}
	if ups != downs {
		t.Fatalf("up/down migration count mismatch: %d up, %d down", ups, downs)
	}
}
