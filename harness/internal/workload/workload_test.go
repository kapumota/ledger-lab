package workload

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kapumota/ledger-lab/harness/internal/history"
)

type memorySink struct {
	mu      sync.Mutex
	records []history.Record
}

func (s *memorySink) Append(r history.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
	return nil
}

func TestRunCountsCancelledInvocationAsUnknown(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	sink := &memorySink{}
	res, err := Run(context.Background(), Config{
		BaseURL: server.URL,
		Accounts: []string{
			"1ff73504-e5de-49fb-882a-7fc09f6e06ce",
			"51392d41-edc2-4a4f-8656-0ed36220a874",
		},
		Currency:    "PEN",
		Profile:     Uniform,
		Concurrency: 1,
		Duration:    50 * time.Millisecond,
		Amount:      100,
		Seed:        17,
		RunID:       "cancelled-run",
		History:     sink,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Sent == 0 {
		t.Fatal("se esperaba al menos una invocación")
	}
	if res.Uncertain != res.Sent {
		t.Fatalf("enviadas=%d inciertas=%d, deben coincidir sin respuesta", res.Sent, res.Uncertain)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if int64(len(sink.records)) != res.Sent {
		t.Fatalf("historia=%d enviadas=%d", len(sink.records), res.Sent)
	}
	for _, r := range sink.records {
		if r.Result != history.Unknown {
			t.Fatalf("resultado=%q, se esperaba unknown", r.Result)
		}
	}
}
