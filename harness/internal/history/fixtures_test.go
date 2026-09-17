package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type fingerprintFixture struct {
	FixtureVersion int `json:"fixture_version"`
	Cases          []struct {
		Name                string  `json:"name"`
		Request             Request `json:"request"`
		ExpectedFingerprint string  `json:"expected_fingerprint"`
	} `json:"cases"`
}

func TestCanonicalPostEntryFingerprintFixture(t *testing.T) {
	data, err := os.ReadFile(fixturePath(t, "post_entry_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture fingerprintFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.FixtureVersion != 1 {
		t.Fatalf("fixture_version=%d, se esperaba 1", fixture.FixtureVersion)
	}
	if len(fixture.Cases) < 3 {
		t.Fatalf("se esperaban al menos tres casos, se obtuvieron %d", len(fixture.Cases))
	}

	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := Fingerprint(tc.Request)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.ExpectedFingerprint {
				t.Fatalf("fingerprint=%s, esperado=%s", got, tc.ExpectedFingerprint)
			}
		})
	}
}

func TestCanonicalHistoryFixture(t *testing.T) {
	f, err := os.Open(fixturePath(t, "history_v1.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	counts := map[Result]int{}
	byKey := map[string][]Record{}
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("línea %d: %v", line, err)
		}
		if err := record.Validate(); err != nil {
			t.Fatalf("línea %d: %v", line, err)
		}
		counts[record.Result]++
		byKey[record.IdempotencyKey] = append(byKey[record.IdempotencyKey], record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if line != 4 || counts[Committed] != 2 || counts[Rejected] != 1 || counts[Unknown] != 1 {
		t.Fatalf("fixture inesperado: líneas=%d resultados=%v", line, counts)
	}

	retry := byKey["7ea4803d-1bb1-4ad4-b923-70bb8a85af43"]
	if len(retry) != 2 {
		t.Fatalf("se esperaban dos invocaciones idempotentes, se obtuvieron %d", len(retry))
	}
	if retry[0].RequestFingerprint != retry[1].RequestFingerprint || retry[0].EntryID == nil ||
		retry[1].EntryID == nil || *retry[0].EntryID != *retry[1].EntryID ||
		retry[0].OperationID == retry[1].OperationID {
		t.Fatal("el fixture no conserva la semántica observable de I3")
	}
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no se pudo localizar el archivo de prueba")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	return filepath.Join(root, "contract", "fixtures", name)
}
