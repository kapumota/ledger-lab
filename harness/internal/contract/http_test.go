package contracttest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type acceptedResponse struct {
	EntryID string `json:"entry_id"`
	Status  string `json:"status"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func TestPostEntryReturnsEquivalentAcceptedResponsesForIdempotentRetry(t *testing.T) {
	env := newTestEnvironment(t)
	key := newUUID(t)
	body := map[string]any{
		"idempotency_key": key,
		"currency":        "PEN",
		"postings": []map[string]any{
			{"account_id": env.funderID, "amount_minor": -100},
			{"account_id": env.accountID, "amount_minor": 100},
		},
		"metadata": map[string]any{"reference": "contrato-http"},
	}

	first := postEntry(t, env.baseURL, body)
	second := postEntry(t, env.baseURL, body)

	if first.status != http.StatusAccepted || second.status != http.StatusAccepted {
		t.Fatalf("se esperaba 202 en ambas respuestas, se obtuvo %d y %d", first.status, second.status)
	}
	firstBody := decodeAccepted(t, first.body)
	secondBody := decodeAccepted(t, second.body)
	if firstBody != secondBody {
		t.Fatalf("las respuestas idempotentes deben ser equivalentes: primera=%+v segunda=%+v", firstBody, secondBody)
	}
	if firstBody.Status != "committed" {
		t.Fatalf("se esperaba estado committed, se obtuvo %q", firstBody.Status)
	}
}

func TestPostEntryRejectsMalformedBody(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("LEDGER_BASE_URL"), "/")
	if baseURL == "" {
		t.Skip("LEDGER_BASE_URL no definida, se omite la prueba de contrato HTTP")
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/entries", strings.NewReader(`{"idempotency_key":`))
	if err != nil {
		t.Fatalf("no se pudo crear la solicitud HTTP: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("falló la solicitud HTTP: %v", err)
	}
	defer resp.Body.Close()

	var body errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("no se pudo decodificar el error: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest || body.Code != "malformed_body" {
		t.Fatalf("se esperaba HTTP 400 malformed_body, se obtuvo %d %q", resp.StatusCode, body.Code)
	}
}

func TestPostEntryMapsContractErrors(t *testing.T) {
	t.Run("idempotency_conflict", func(t *testing.T) {
		env := newTestEnvironment(t)
		key := newUUID(t)
		body := map[string]any{
			"idempotency_key": key,
			"currency":        "PEN",
			"postings": []map[string]any{
				{"account_id": env.funderID, "amount_minor": -100},
				{"account_id": env.accountID, "amount_minor": 100},
			},
			"metadata": map[string]any{"reference": "A"},
		}
		if got := postEntry(t, env.baseURL, body); got.status != http.StatusAccepted {
			t.Fatalf("precondición: se esperaba 202, se obtuvo %d", got.status)
		}
		body["metadata"] = map[string]any{"reference": "B"}
		assertContractError(t, postEntry(t, env.baseURL, body), http.StatusConflict, "idempotency_conflict")
	})

	t.Run("unknown_account", func(t *testing.T) {
		env := newTestEnvironment(t)
		body := map[string]any{
			"idempotency_key": newUUID(t),
			"currency":        "PEN",
			"postings": []map[string]any{
				{"account_id": env.funderID, "amount_minor": -100},
				{"account_id": newUUID(t), "amount_minor": 100},
			},
		}
		assertContractError(t, postEntry(t, env.baseURL, body), http.StatusUnprocessableEntity, "unknown_account")
	})

	t.Run("invalid_entry", func(t *testing.T) {
		env := newTestEnvironment(t)
		body := map[string]any{
			"idempotency_key": newUUID(t),
			"currency":        "PEN",
			"postings": []map[string]any{
				{"account_id": env.funderID, "amount_minor": -100},
			},
		}
		assertContractError(t, postEntry(t, env.baseURL, body), http.StatusUnprocessableEntity, "invalid_entry")
	})

	t.Run("insufficient_funds", func(t *testing.T) {
		env := newTestEnvironment(t)
		body := map[string]any{
			"idempotency_key": newUUID(t),
			"currency":        "PEN",
			"postings": []map[string]any{
				{"account_id": env.accountID, "amount_minor": -1},
				{"account_id": env.funderID, "amount_minor": 1},
			},
		}
		assertContractError(t, postEntry(t, env.baseURL, body), http.StatusConflict, "insufficient_funds")
	})
}

type testEnvironment struct {
	baseURL   string
	funderID  string
	accountID string
}

func newTestEnvironment(t *testing.T) testEnvironment {
	t.Helper()
	baseURL := strings.TrimRight(os.Getenv("LEDGER_BASE_URL"), "/")
	dsn := os.Getenv("DATABASE_URL")
	if baseURL == "" || dsn == "" {
		t.Skip("LEDGER_BASE_URL o DATABASE_URL no definida, se omite la prueba de contrato HTTP")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("no se pudo abrir PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)

	funderID := newUUID(t)
	accountID := newUUID(t)
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, name, currency, kind, constrained)
		VALUES ($1, $2, 'PEN', 'equity', FALSE),
		       ($3, $4, 'PEN', 'liability', TRUE)`,
		funderID, "funder_http_"+funderID, accountID, "account_http_"+accountID); err != nil {
		t.Fatalf("no se pudieron crear las cuentas de prueba: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO balances (account_id, amount_minor)
		VALUES ($1, 0), ($2, 0)`, funderID, accountID); err != nil {
		t.Fatalf("no se pudieron crear los saldos de prueba: %v", err)
	}

	return testEnvironment{baseURL: baseURL, funderID: funderID, accountID: accountID}
}

type httpResult struct {
	status int
	body   []byte
}

func postEntry(t *testing.T, baseURL string, body any) httpResult {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar la solicitud: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/entries", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("no se pudo crear la solicitud HTTP: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("falló la solicitud HTTP: %v", err)
	}
	defer resp.Body.Close()

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("no se pudo leer la respuesta HTTP: %v", err)
	}
	return httpResult{status: resp.StatusCode, body: append([]byte(nil), raw...)}
}

func decodeAccepted(t *testing.T, payload []byte) acceptedResponse {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("no se pudo decodificar la respuesta aceptada: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("la respuesta aceptada debe exponer solo entry_id y status: %s", payload)
	}
	if _, ok := raw["entry_id"]; !ok {
		t.Fatalf("falta entry_id en la respuesta: %s", payload)
	}
	if _, ok := raw["status"]; !ok {
		t.Fatalf("falta status en la respuesta: %s", payload)
	}

	var body acceptedResponse
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("no se pudo materializar la respuesta aceptada: %v", err)
	}
	return body
}

func assertContractError(t *testing.T, got httpResult, status int, code string) {
	t.Helper()
	if got.status != status {
		t.Fatalf("se esperaba HTTP %d, se obtuvo %d", status, got.status)
	}
	var body errorResponse
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("no se pudo decodificar el error: %v", err)
	}
	if body.Code != code {
		t.Fatalf("se esperaba code=%q, se obtuvo %q", code, body.Code)
	}
}

func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("no se pudo generar UUID: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hexValue[0:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:32])
}
