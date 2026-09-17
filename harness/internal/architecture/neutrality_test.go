package architecture_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestNeutralTreesDoNotImportRuntimeImplementations(t *testing.T) {
	root := repositoryRoot(t)
	forbidden := []string{
		"github.com/kapumota/ledger-lab/go",
		"github.com/kapumota/ledger-lab/beam",
	}

	for _, dir := range []string{"contract", "harness", "experiments"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == ".git" || entry.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}

			switch entry.Name() {
			case "go.mod", "go.work":
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				for _, prefix := range forbidden {
					if strings.Contains(string(data), prefix) {
						t.Errorf("%s referencia implementación prohibida %q", relative(root, path), prefix)
					}
				}
				return nil
			}

			if filepath.Ext(path) != ".go" {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				for _, prefix := range forbidden {
					if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
						t.Errorf("%s importa implementación prohibida %q", relative(root, path), importPath)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("no se pudo inspeccionar %s: %v", dir, err)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no se pudo localizar el archivo de prueba")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
