package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFingerprintIgnoresPostingAndObjectKeyOrder(t *testing.T) {
	a := Request{
		IdempotencyKey: "7EA4803D-1BB1-4AD4-B923-70BB8A85AF43",
		Currency:       " pen ",
		Postings: []Posting{
			{AccountID: "51392d41-edc2-4a4f-8656-0ed36220a874", AmountMinor: 10050},
			{AccountID: "1ff73504-e5de-49fb-882a-7fc09f6e06ce", AmountMinor: -10050},
		},
		Metadata: json.RawMessage(`{"b":2,"a":{"y":1,"x":[3,2,1]}}`),
	}
	b := Request{
		IdempotencyKey: "7ea4803d1bb14ad4b92370bb8a85af43",
		Currency:       "PEN",
		Postings: []Posting{
			{AccountID: "1FF73504-E5DE-49FB-882A-7FC09F6E06CE", AmountMinor: -10050},
			{AccountID: "51392d41-edc2-4a4f-8656-0ed36220a874", AmountMinor: 10050},
		},
		Metadata: json.RawMessage(`{"a":{"x":[3,2,1],"y":1},"b":2}`),
	}

	fa, err := Fingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := Fingerprint(b)
	if err != nil {
		t.Fatal(err)
	}
	if fa != fb {
		t.Fatalf("fingerprints equivalentes difieren:\n%s\n%s", fa, fb)
	}
}

func TestFingerprintChangesWhenMetadataChanges(t *testing.T) {
	base := Request{
		IdempotencyKey: "7ea4803d-1bb1-4ad4-b923-70bb8a85af43",
		Currency:       "PEN",
		Postings: []Posting{
			{AccountID: "1ff73504-e5de-49fb-882a-7fc09f6e06ce", AmountMinor: -100},
			{AccountID: "51392d41-edc2-4a4f-8656-0ed36220a874", AmountMinor: 100},
		},
		Metadata: json.RawMessage(`{"source":"a"}`),
	}
	changed := base
	changed.Metadata = json.RawMessage(`{"source":"b"}`)

	a, err := Fingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Fingerprint(changed)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("metadata distinta produjo el mismo fingerprint")
	}
}

func TestOperationIDIsStableAndSeqSensitive(t *testing.T) {
	a := OperationID("run-1", "client-001", 7)
	b := OperationID("run-1", "client-001", 7)
	c := OperationID("run-1", "client-001", 8)

	if a != b {
		t.Fatal("la misma invocación no produjo operation_id estable")
	}
	if a == c {
		t.Fatal("client_seq distinto produjo el mismo operation_id")
	}
}

func TestNDJSONWriterWritesOneValidRecordPerLine(t *testing.T) {
	var out bytes.Buffer
	w := NewNDJSONWriter(&out)

	entryID := "a650ba53-cba7-4b57-bf9a-994d2e98b443"
	fp := "sha256:" + strings.Repeat("a", 64)
	records := []Record{
		{
			SchemaVersion: 1, RunID: "run-1", OperationID: OperationID("run-1", "client-000", 0),
			ClientID: "client-000", ClientSeq: 0,
			IdempotencyKey:     "7ea4803d-1bb1-4ad4-b923-70bb8a85af43",
			RequestFingerprint: fp, InvokeNS: 10, CompleteNS: 20,
			Result: Committed, EntryID: &entryID,
		},
		{
			SchemaVersion: 1, RunID: "run-1", OperationID: OperationID("run-1", "client-000", 1),
			ClientID: "client-000", ClientSeq: 1,
			IdempotencyKey:     "7ea4803d-1bb1-4ad4-b923-70bb8a85af43",
			RequestFingerprint: fp, InvokeNS: 21, CompleteNS: 30,
			Result: Rejected, ErrorCode: ptr("idempotency_conflict"),
		},
	}

	for _, r := range records {
		if err := w.Append(r); err != nil {
			t.Fatal(err)
		}
	}

	scanner := bufio.NewScanner(bytes.NewReader(out.Bytes()))
	var got int
	for scanner.Scan() {
		var r Record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		got++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if got != len(records) {
		t.Fatalf("se esperaban %d líneas, se obtuvieron %d", len(records), got)
	}
}

func ptr(s string) *string { return &s }
