package verify

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kapumota/ledger-lab/harness/internal/history"
)

type historyEvidence struct {
	Inspected  int64
	EntryToKey map[string]string
}

// CheckHistory contrasta una historia neutral con el estado persistido sin
// modificar el archivo observado. Los resultados unknown permanecen unknown.
func CheckHistory(ctx context.Context, pool *pgxpool.Pool, r io.Reader) (CheckResult, []Violation, error) {
	evidence, violations, err := scanHistory(r)
	if err != nil {
		return CheckResult{}, nil, err
	}

	dbViolations, err := reconcileCommitted(ctx, pool, evidence.EntryToKey)
	if err != nil {
		return CheckResult{}, nil, err
	}
	violations = append(violations, dbViolations...)

	return CheckResult{
		Invariant:  "I3",
		Name:       "historia neutral consistente con el ledger",
		Inspected:  evidence.Inspected,
		Violations: len(violations),
	}, violations, nil
}

func scanHistory(r io.Reader) (historyEvidence, []Violation, error) {
	evidence := historyEvidence{EntryToKey: map[string]string{}}
	operationIDs := map[string]struct{}{}
	clientSeqs := map[string]map[uint64]struct{}{}
	type committedValue struct {
		fingerprint string
		entryID     string
	}
	committedByKey := map[string]committedValue{}
	var violations []Violation

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			return evidence, violations, fmt.Errorf("historia: línea %d vacía", line)
		}

		dec := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		dec.DisallowUnknownFields()
		var record history.Record
		if err := dec.Decode(&record); err != nil {
			return evidence, violations, fmt.Errorf("historia: línea %d: %w", line, err)
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			if err == nil {
				err = fmt.Errorf("más de un valor JSON")
			}
			return evidence, violations, fmt.Errorf("historia: línea %d: %w", line, err)
		}
		if err := record.Validate(); err != nil {
			return evidence, violations, fmt.Errorf("historia: línea %d: %w", line, err)
		}

		if _, exists := operationIDs[record.OperationID]; exists {
			return evidence, violations, fmt.Errorf("historia: operation_id duplicado %s", record.OperationID)
		}
		operationIDs[record.OperationID] = struct{}{}
		if clientSeqs[record.ClientID] == nil {
			clientSeqs[record.ClientID] = map[uint64]struct{}{}
		}
		if _, exists := clientSeqs[record.ClientID][record.ClientSeq]; exists {
			return evidence, violations, fmt.Errorf("historia: client_seq duplicado para %s: %d", record.ClientID, record.ClientSeq)
		}
		clientSeqs[record.ClientID][record.ClientSeq] = struct{}{}
		evidence.Inspected++

		if record.Result != history.Committed {
			continue
		}
		entryID := *record.EntryID
		if previous, ok := committedByKey[record.IdempotencyKey]; ok {
			if previous.fingerprint != record.RequestFingerprint || previous.entryID != entryID {
				violations = append(violations, Violation{
					Invariant: "I3",
					Subject:   record.IdempotencyKey,
					Detail:    "invocaciones committed de la misma clave no conservan fingerprint y entry_id",
				})
			}
		} else {
			committedByKey[record.IdempotencyKey] = committedValue{
				fingerprint: record.RequestFingerprint,
				entryID:     entryID,
			}
		}
		if key, ok := evidence.EntryToKey[entryID]; ok && key != record.IdempotencyKey {
			violations = append(violations, Violation{
				Invariant: "I3",
				Subject:   entryID,
				Detail:    "el mismo entry_id aparece asociado a claves idempotentes distintas en la historia",
			})
		} else {
			evidence.EntryToKey[entryID] = record.IdempotencyKey
		}
	}
	if err := scanner.Err(); err != nil {
		return evidence, violations, err
	}
	if evidence.Inspected == 0 {
		return evidence, violations, fmt.Errorf("historia: no contiene registros")
	}
	return evidence, violations, nil
}

func reconcileCommitted(ctx context.Context, pool *pgxpool.Pool, expected map[string]string) ([]Violation, error) {
	if len(expected) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	const batchSize = 500
	var violations []Violation
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		rows, err := pool.Query(ctx, `
			SELECT id::text, idempotency_key::text
			  FROM entries
			 WHERE id::text = ANY($1::text[])`, batch)
		if err != nil {
			return nil, err
		}

		found := map[string]string{}
		for rows.Next() {
			var id, key string
			if err := rows.Scan(&id, &key); err != nil {
				rows.Close()
				return nil, err
			}
			found[id] = key
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()

		for _, id := range batch {
			actualKey, ok := found[id]
			if !ok {
				violations = append(violations, Violation{
					Invariant: "I3",
					Subject:   id,
					Detail:    "la historia reporta committed pero el entry_id no existe en el ledger",
				})
				continue
			}
			if actualKey != expected[id] {
				violations = append(violations, Violation{
					Invariant: "I3",
					Subject:   id,
					Detail: fmt.Sprintf("idempotency_key en ledger=%s, historia=%s",
						actualKey, expected[id]),
				})
			}
		}
	}
	return violations, nil
}
