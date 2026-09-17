package verify

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestScanCanonicalHistoryFixture(t *testing.T) {
	f, err := os.Open(verifyFixturePath(t, "history_v1.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	evidence, violations, err := scanHistory(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("fixture canónico produjo violaciones: %+v", violations)
	}
	if evidence.Inspected != 4 {
		t.Fatalf("inspeccionados=%d, se esperaban 4", evidence.Inspected)
	}
	if len(evidence.EntryToKey) != 1 {
		t.Fatalf("committed únicos=%d, se esperaba 1 entry_id", len(evidence.EntryToKey))
	}
}

func verifyFixturePath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no se pudo localizar el archivo de prueba")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	return filepath.Join(root, "contract", "fixtures", name)
}
