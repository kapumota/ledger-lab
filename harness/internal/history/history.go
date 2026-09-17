// Package history implementa la historia neutral definida en contract/history.md.
//
// La historia pertenece al arnés. No importa código de ninguna implementación y
// registra únicamente lo observado en la frontera HTTP.
package history

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Result es el desenlace observable de una invocación.
type Result string

const (
	Committed Result = "committed"
	Rejected  Result = "rejected"
	Unknown   Result = "unknown"
)

// Posting es la representación neutral de una línea de PostEntry.
type Posting struct {
	AccountID   string `json:"account_id"`
	AmountMinor int64  `json:"amount_minor"`
}

// Request contiene únicamente campos que participan en la equivalencia
// contractual de PostEntry.
type Request struct {
	IdempotencyKey string          `json:"idempotency_key"`
	Currency       string          `json:"currency"`
	Postings       []Posting       `json:"postings"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

// Record es una línea de history.ndjson.
type Record struct {
	SchemaVersion      int     `json:"schema_version"`
	RunID              string  `json:"run_id"`
	OperationID        string  `json:"operation_id"`
	ClientID           string  `json:"client_id"`
	ClientSeq          uint64  `json:"client_seq"`
	IdempotencyKey     string  `json:"idempotency_key"`
	RequestFingerprint string  `json:"request_fingerprint"`
	InvokeNS           int64   `json:"invoke_ns"`
	CompleteNS         int64   `json:"complete_ns"`
	Result             Result  `json:"result"`
	EntryID            *string `json:"entry_id"`
	ErrorCode          *string `json:"error_code"`
}

// Sink recibe registros inmutables de historia.
type Sink interface {
	Append(Record) error
}

// NDJSONWriter serializa un registro JSON por línea y es seguro para uso
// concurrente por los workers del workload.
type NDJSONWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewNDJSONWriter construye un sink sobre w.
func NewNDJSONWriter(w io.Writer) *NDJSONWriter {
	return &NDJSONWriter{w: w}
}

// Append valida y escribe un registro completo.
func (w *NDJSONWriter) Append(r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return json.NewEncoder(w.w).Encode(r)
}

// Validate comprueba las reglas estructurales de history.md.
func (r Record) Validate() error {
	if r.SchemaVersion != 1 {
		return fmt.Errorf("history: schema_version debe ser 1")
	}
	if r.RunID == "" || r.OperationID == "" || r.ClientID == "" ||
		r.IdempotencyKey == "" || r.RequestFingerprint == "" {
		return fmt.Errorf("history: faltan campos obligatorios")
	}
	if r.InvokeNS < 0 || r.CompleteNS < r.InvokeNS {
		return fmt.Errorf("history: intervalo temporal inválido")
	}
	if !strings.HasPrefix(r.RequestFingerprint, "sha256:") ||
		len(r.RequestFingerprint) != len("sha256:")+64 {
		return fmt.Errorf("history: fingerprint inválido")
	}

	switch r.Result {
	case Committed:
		if r.EntryID == nil || *r.EntryID == "" {
			return fmt.Errorf("history: committed requiere entry_id")
		}
	case Rejected:
		if r.ErrorCode == nil || *r.ErrorCode == "" {
			return fmt.Errorf("history: rejected requiere error_code")
		}
	case Unknown:
		if r.ErrorCode == nil || *r.ErrorCode == "" {
			return fmt.Errorf("history: unknown requiere error_code")
		}
	default:
		return fmt.Errorf("history: resultado desconocido %q", r.Result)
	}
	return nil
}

// OperationID deriva un UUID estable para una invocación sin consumir el PRNG
// del workload. Así P10 no altera la secuencia lógica generada por una semilla.
func OperationID(runID, clientID string, seq uint64) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + clientID + "\x00" + strconv.FormatUint(seq, 10)))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Fingerprint implementa el fingerprint neutral descrito en post_entry.md.
func Fingerprint(req Request) (string, error) {
	key, err := canonicalUUID(req.IdempotencyKey)
	if err != nil {
		return "", fmt.Errorf("history: idempotency_key: %w", err)
	}

	type posting struct {
		account string
		amount  int64
	}
	ps := make([]posting, 0, len(req.Postings))
	for _, p := range req.Postings {
		account, err := canonicalUUID(p.AccountID)
		if err != nil {
			return "", fmt.Errorf("history: account_id: %w", err)
		}
		ps = append(ps, posting{account: account, amount: p.AmountMinor})
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].account != ps[j].account {
			return ps[i].account < ps[j].account
		}
		return ps[i].amount < ps[j].amount
	})

	metadata, err := canonicalMetadata(req.Metadata)
	if err != nil {
		return "", fmt.Errorf("history: metadata: %w", err)
	}

	var b bytes.Buffer
	b.WriteString(`{"currency":`)
	writeJSONString(&b, strings.ToUpper(strings.TrimSpace(req.Currency)))
	b.WriteString(`,"idempotency_key":`)
	writeJSONString(&b, key)
	b.WriteString(`,"metadata":`)
	b.Write(metadata)
	b.WriteString(`,"postings":[`)
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"account_id":`)
		writeJSONString(&b, p.account)
		b.WriteString(`,"amount_minor":`)
		b.WriteString(strconv.FormatInt(p.amount, 10))
		b.WriteByte('}')
	}
	b.WriteString(`]}`)

	sum := sha256.Sum256(b.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalUUID(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "urn:uuid:")
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		s = s[1 : len(s)-1]
	}
	raw := strings.ReplaceAll(s, "-", "")
	if len(raw) != 32 {
		return "", fmt.Errorf("UUID inválido %q", s)
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", fmt.Errorf("UUID inválido %q", s)
	}
	return raw[0:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:32], nil
}

func canonicalMetadata(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("metadata contiene más de un valor JSON")
		}
		return nil, err
	}
	var b bytes.Buffer
	if err := writeCanonicalJSON(&b, value); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeCanonicalJSON(b *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeJSONString(b, v)
	case json.Number:
		b.WriteString(v.String())
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonicalJSON(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, k)
			b.WriteByte(':')
			if err := writeCanonicalJSON(b, v[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("tipo JSON no soportado %T", value)
	}
	return nil
}

func writeJSONString(b *bytes.Buffer, s string) {
	encoded, _ := json.Marshal(s)
	b.Write(encoded)
}
