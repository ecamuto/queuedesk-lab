package httpapi

// Un middleware è una funzione che riceve un http.Handler e ne restituisce
// un altro che lo "avvolge": fa qualcosa prima e/o dopo aver chiamato
// next.ServeHTTP. In NewHandler (server.go) si impilano uno sull'altro.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"
)

// ctxKey è un tipo non esportato: nessun altro package può leggere o
// sovrascrivere i valori che mettiamo nel context.
//
// Go: "type ctxKey int" crea un tipo nuovo, distinto da int. Due chiavi del
// context sono uguali solo se hanno stesso tipo e stesso valore: un altro
// package potrebbe usare 0 o "request_id", ma mai un nostro ctxKey.
type ctxKey int

const requestIDKey ctxKey = 0

// RequestIDFrom restituisce il request ID della richiesta corrente.
func RequestIDFrom(ctx context.Context) string {
	// TODO(BASE-3, passo A) — Leggere il request ID senza rischiare un panic.
	//
	// ctx.Value(requestIDKey) restituisce any. Converti il valore in string con
	// la type assertion sicura a due risultati:
	//
	//     id, ok := valore.(string)
	//
	// Qui ok non serve e può diventare _. Se chiave manca o valore non è string,
	// id deve restare "". Non usare l'assertion a un risultato: può fare panic.
	// Sostituisci il return provvisorio.
	// Fatto quando: TestRequestIDInLogs non produce panic anche senza request ID.
	return ""
}

const requestIDHeader = "X-Request-ID"

// validRequestID accetta solo ID brevi e senza caratteri strani: l'ID del
// client finisce nei log, quindi non ci fidiamo ciecamente.
//
// Go: una variabile a livello di package, inizializzata una volta all'avvio.
// MustCompile va in panic se la regex è sbagliata: va bene qui, perché è
// una costante scritta da noi e l'errore si vedrebbe al primo avvio.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// requestID riusa l'X-Request-ID del client se ben formato, altrimenti ne
// genera uno; lo rimanda nella risposta e lo salva nel context.
func requestID(next http.Handler) http.Handler {
	// TODO(BASE-3, passo B) — Costruire il middleware request ID.
	//
	// Modello mentale:
	//
	//   richiesta → requestID → next handler → risposta
	//
	// requestID deve restituire un http.HandlerFunc. Il codice dentro la
	// funzione anonima gira una volta per richiesta e può chiamare next.
	//
	// Per ogni richiesta:
	//   1. leggi X-Request-ID con r.Header.Get(requestIDHeader);
	//   2. se non passa validRequestID, sostituiscilo con newRequestID();
	//   3. mettilo nell'header della risposta;
	//   4. aggiungilo al context con context.WithValue;
	//   5. chiama next.ServeHTTP con r.WithContext(ctx), non con r originale.
	//
	// Sostituisci "return next": così com'è, il middleware non fa nulla.
	// Fatto quando: make test-acceptance RUN=TestRequestID.
	return next
}

// newRequestID genera 8 byte casuali in esadecimale (16 caratteri).
func newRequestID() string {
	b := make([]byte, 8) // Go: make crea una slice di 8 byte a zero
	_, _ = rand.Read(b)  // crypto/rand.Read non fallisce (Go 1.24+)
	return hex.EncodeToString(b)
}

// statusRecorder cattura status e byte scritti per il log di accesso.
//
// Go: "http.ResponseWriter" senza nome di campo è un campo incorporato
// (embedding): statusRecorder ottiene tutti i suoi metodi, quindi è a sua
// volta un http.ResponseWriter. Ridefiniamo solo WriteHeader e Write.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code) // chiama il metodo "originale"
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	// Chi scrive il body senza WriteHeader ottiene implicitamente 200.
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap permette a http.ResponseController di raggiungere il writer originale.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// logRequests scrive una riga per richiesta. Non registra header né body:
// conterrebbero token e password.
//
// Ha la stessa forma di requestID: riceve next, restituisce una closure.
// La differenza è che lavora anche DOPO next.ServeHTTP.
func (a *api) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w} // Go: & prende l'indirizzo: rec è un puntatore
		next.ServeHTTP(rec, r)                    // qui girano mux e handler
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		a.log.InfoContext(r.Context(), "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"route", r.Pattern, // valorizzato dal ServeMux (Go 1.23+)
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", float64(time.Since(start).Microseconds())/1000,
		)
	})
}

// recoverPanics trasforma un panic (bug) in un 500 invece di far cadere la
// connessione. Gli errori operativi non usano panic: arrivano come error.
func (a *api) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Go: una funzione differita (defer) gira anche durante un panic.
		// Dentro di lei recover() ferma il panic e ne restituisce il valore
		// (nil se non c'era nessun panic).
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v) // panic "voluto" da net/http: lo lasciamo passare
				}
				a.log.ErrorContext(r.Context(), "panic in handler", "panic", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// LogHandler avvolge un slog.Handler aggiungendo request_id a ogni record
// emesso con un context che lo contiene (es. log.InfoContext(r.Context(), ...)).
//
// Go: anche qui embedding: LogHandler è uno slog.Handler perché ne incorpora
// uno, e sovrascrive solo i metodi che servono.
type LogHandler struct {
	slog.Handler
}

// NewLogHandler crea il wrapper.
func NewLogHandler(h slog.Handler) *LogHandler { return &LogHandler{Handler: h} }

// Handle implementa slog.Handler.
func (h *LogHandler) Handle(ctx context.Context, rec slog.Record) error {
	if id := RequestIDFrom(ctx); id != "" {
		rec.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, rec)
}

// WithAttrs implementa slog.Handler mantenendo il wrapper.
func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup implementa slog.Handler mantenendo il wrapper.
func (h *LogHandler) WithGroup(name string) slog.Handler {
	return &LogHandler{Handler: h.Handler.WithGroup(name)}
}
