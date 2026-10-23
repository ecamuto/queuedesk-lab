package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"queuedesk/internal/httpapi"
)

// fakePinger sostituisce il database nei test. Ha il metodo Ping, quindi
// soddisfa httpapi.Pinger come *pgxpool.Pool. Con err valorizzato simula
// un database irraggiungibile.
type fakePinger struct{ err error }

// Go: un parametro senza nome (context.Context) è lecito quando non lo si usa.
func (p fakePinger) Ping(context.Context) error { return p.err }

// option configura le dipendenze dell'handler sotto test. Ogni tappa
// aggiunge le sue (withAuth, withTickets) senza cambiare i test esistenti.
type option func(*httpapi.Deps)

func withDB(p fakePinger) option { return func(d *httpapi.Deps) { d.DB = p } }

func withLogger(l *slog.Logger) option { return func(d *httpapi.Deps) { d.Logger = l } }

// newHandler costruisce l'handler da testare: dipendenze finte di default,
// sovrascrivibili con le option, es. newHandler(withDB(fakePinger{err: ...})).
func newHandler(opts ...option) http.Handler {
	d := httpapi.Deps{DB: fakePinger{}, Logger: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(&d)
	}
	return httpapi.NewHandler(d)
}

type reqOpt func(*http.Request)

func header(k, v string) reqOpt {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

// serve esegue una richiesta sull'handler. Se c'è un body, imposta
// Content-Type: application/json (sovrascrivibile con header()).
//
// Go: net/http/httptest permette di testare un handler senza aprire porte:
// NewRequest crea una richiesta finta, NewRecorder registra status, header
// e body della risposta.
func serve(h http.Handler, method, target, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// assertError verifica status, envelope e codice di errore.
func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	// Go: t.Helper() fa sì che un errore venga riportato alla riga del test
	// che ha chiamato assertError, non a una riga qui dentro.
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the error envelope: %v (%s)", err, rec.Body)
	}
	if body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error = %+v, want code %q and a message", body.Error, code)
	}
}

// decodeBody decodifica il body JSON nel tipo T.
//
// Go: [T any] è un parametro di tipo (generics): decodeBody[map[string]string]
// restituisce una mappa, decodeBody[ticketJSON] una struct.
func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rec.Body)
	}
	return v
}
