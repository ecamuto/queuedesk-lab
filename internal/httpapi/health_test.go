//go:build acceptance

// Acceptance test della tappa corrente: girano con `make test-acceptance`.
// Falliscono finché i TODO della tappa non sono completati (vedi TAPPA.md).

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"queuedesk/internal/httpapi"
)

// Questi test sono il contratto di BASE-2 e BASE-3: leggili prima di
// scrivere il codice. Nessuno apre una porta o un database: newHandler usa
// fakePinger e serve usa httptest (vedi helpers_test.go).

func TestHealthz(t *testing.T) {
	// Il pinger fallisce: la liveness non deve dipendere dal database.
	h := newHandler(withDB(fakePinger{err: errors.New("db down")}))
	rec := serve(h, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := decodeBody[map[string]string](t, rec)["status"]; got != "ok" {
		t.Fatalf(`status field = %q, want "ok"`, got)
	}
}

func TestHealthzMethodNotAllowed(t *testing.T) {
	// Il metodo nel pattern ("GET /healthz") basta al ServeMux per il 405.
	rec := serve(newHandler(), http.MethodPost, "/healthz", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// Go: due t.Run nello stesso test raggruppano casi diversi dello stesso
// comportamento; nell'output compaiono come TestReadyz/database_down.
func TestReadyz(t *testing.T) {
	t.Run("database reachable", func(t *testing.T) {
		rec := serve(newHandler(), http.MethodGet, "/readyz", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := decodeBody[map[string]string](t, rec)["status"]; got != "ready" {
			t.Fatalf(`status field = %q, want "ready"`, got)
		}
	})
	t.Run("database down", func(t *testing.T) {
		rec := serve(newHandler(withDB(fakePinger{err: errors.New("db down")})), http.MethodGet, "/readyz", "")
		assertError(t, rec, http.StatusServiceUnavailable, "not_ready")
	})
}

func TestRequestID(t *testing.T) {
	h := newHandler()

	generated := serve(h, http.MethodGet, "/healthz", "").Header().Get("X-Request-ID")
	if generated == "" {
		t.Fatal("missing generated X-Request-ID")
	}
	propagated := serve(h, http.MethodGet, "/healthz", "", header("X-Request-ID", "abc-123")).Header().Get("X-Request-ID")
	if propagated != "abc-123" {
		t.Fatalf("X-Request-ID = %q, want the client value", propagated)
	}
	unsafe := serve(h, http.MethodGet, "/healthz", "", header("X-Request-ID", strings.Repeat("x", 200))).Header().Get("X-Request-ID")
	if len(unsafe) > 64 {
		t.Fatal("oversized client request ID must be replaced")
	}
}

func TestRequestIDInLogs(t *testing.T) {
	// Il log di accesso usa RequestIDFrom tramite httpapi.LogHandler: se il
	// request ID non arriva nel context, la riga di log non lo contiene.
	// Go: bytes.Buffer è un io.Writer in memoria: il logger scrive lì invece
	// che su stdout, e il test può leggere cosa è stato loggato.
	var buf bytes.Buffer
	log := slog.New(httpapi.NewLogHandler(slog.NewJSONHandler(&buf, nil)))
	serve(newHandler(withLogger(log)), http.MethodGet, "/healthz", "", header("X-Request-ID", "trace-42"))

	var line struct {
		Msg       string `json:"msg"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log is not a single JSON line: %v (%s)", err, buf.String())
	}
	if line.Msg != "http request" || line.RequestID != "trace-42" {
		t.Fatalf("log line = %+v, want msg \"http request\" with request_id \"trace-42\"", line)
	}
}
