package policy_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kind service packages must not import pgxpool; they take policy.Scope only.
func TestKindPackagesDoNotImportPgxpool(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := dir
	for {
		if _, err := os.Stat(filepath.Join(repo, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(repo)
		if parent == repo {
			t.Fatal("go.mod not found")
		}
		repo = parent
	}

	roots := []string{
		"internal/variable",
		"internal/counter",
		"internal/sqltables",
		"internal/queue",
		"internal/bus",
		"internal/gitstore",
	}
	fset := token.NewFileSet()
	checkFile := func(path string) {
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "github.com/jackc/pgx/v5/pgxpool" {
				t.Errorf("%s imports pgxpool", path)
			}
		}
	}
	for _, root := range roots {
		err := filepath.Walk(filepath.Join(repo, root), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			checkFile(path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"internal/schedule/service.go", "internal/logstore/service.go"} {
		checkFile(filepath.Join(repo, rel))
	}
}
