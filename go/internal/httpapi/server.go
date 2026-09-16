// Package httpapi expone el contrato PostEntry sobre HTTP.
//
// El contrato es lo unico que ambas implementaciones comparten en la ruta de
// escritura. Cualquier cambio aqui invalida la comparacion, asi que la suite de
// pruebas de contrato debe ejecutarse contra los dos brazos en el mismo job de
// integracion continua.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/kapumota/ledger-lab/go/internal/domain"
	"github.com/kapumota/ledger-lab/go/internal/money"
	"github.com/kapumota/ledger-lab/go/internal/store"
)

// PostingDTO es la representacion externa de una linea del asiento.
type PostingDTO struct {
	AccountID string `json:"account_id"`
	Amount    int64  `json:"amount_minor"`
}

// PostEntryRequest es el cuerpo del comando.
type PostEntryRequest struct {
	IdempotencyKey string          `json:"idempotency_key"`
	Currency       string          `json:"currency"`
	Postings       []PostingDTO    `json:"postings"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

// PostEntryResponse es la respuesta del comando.
type PostEntryResponse struct {
	EntryID string `json:"entry_id"`
	Status  string `json:"status"`
}

// ErrorResponse es el cuerpo de error. El campo code es estable y forma parte
// del contrato, el campo message no lo es.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Server implementa el contrato sobre un Store.
type Server struct {
	st *store.Store
}

func New(st *store.Store) *Server { return &Server{st: st} }

// Routes registra el contrato. Se usa el enrutador de la biblioteca estandar
// con patrones por metodo, disponible desde Go 1.22, para no introducir una
// dependencia de enrutamiento que despues habria que justificar.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /entries", s.postEntry)
	mux.HandleFunc("GET /accounts/{id}/balance", s.getBalance)
	mux.HandleFunc("GET /metrics", s.getMetrics)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func (s *Server) postEntry(w http.ResponseWriter, r *http.Request) {
	var req PostEntryRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed_body", err.Error())
		return
	}

	e, err := aDominio(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_entry", err.Error())
		return
	}

	res, err := s.st.PostEntry(r.Context(), e)
	switch {
	case err == nil:
		// La primera aplicación y un reintento legítimo comparten exactamente
		// el mismo contrato observable. La distinción permanece solo en las
		// métricas internas del Store.
		writeJSON(w, http.StatusAccepted, PostEntryResponse{
			EntryID: res.EntryID.String(),
			Status:  "committed",
		})
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, store.ErrInsufficientFunds):
		writeError(w, http.StatusConflict, "insufficient_funds", err.Error())
	case errors.Is(err, store.ErrAccountNotFound):
		writeError(w, http.StatusUnprocessableEntity, "unknown_account", err.Error())
	case errors.Is(err, store.ErrRetriesExhausted):
		writeError(w, http.StatusServiceUnavailable, "retries_exhausted", err.Error())
	case errors.Is(err, domain.ErrUnbalanced),
		errors.Is(err, domain.ErrTooFewPostings),
		errors.Is(err, domain.ErrZeroAmount):
		writeError(w, http.StatusUnprocessableEntity, "invalid_entry", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func (s *Server) getBalance(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_account_id", err.Error())
		return
	}
	saldo, err := s.st.Balance(r.Context(), id)
	if errors.Is(err, store.ErrAccountNotFound) {
		writeError(w, http.StatusNotFound, "unknown_account", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account_id":   id.String(),
		"amount_minor": int64(saldo),
	})
}

func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.st.Metrics.Snapshot())
}

func aDominio(req PostEntryRequest) (domain.Entry, error) {
	key, err := uuid.Parse(req.IdempotencyKey)
	if err != nil {
		return domain.Entry{}, err
	}
	cur, err := money.ParseCurrency(req.Currency)
	if err != nil {
		return domain.Entry{}, err
	}
	ps := make([]domain.Posting, 0, len(req.Postings))
	for _, p := range req.Postings {
		acc, err := uuid.Parse(p.AccountID)
		if err != nil {
			return domain.Entry{}, err
		}
		ps = append(ps, domain.Posting{AccountID: acc, Amount: money.Minor(p.Amount)})
	}
	e := domain.Entry{
		ID:             uuid.New(),
		IdempotencyKey: key,
		Currency:       cur,
		Postings:       ps,
		Metadata:       req.Metadata,
	}
	return e, e.Validate()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, ErrorResponse{Code: kind, Message: msg})
}

// ReadHeaderTimeout razonable para no dejar el servidor expuesto a Slowloris en
// las campanas de carga.
const ReadHeaderTimeout = 5 * time.Second
