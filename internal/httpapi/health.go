package httpapi

import (
	"net/http"
	"time"
)

// readyTimeout è breve: un probe di readiness non deve restare appeso.
const readyTimeout = 2 * time.Second

// healthz è la liveness: il processo risponde. Non tocca il database,
// altrimenti un DB lento farebbe riavviare un'applicazione sana.
//
// Go: ogni handler HTTP ha questa firma: riceve un http.ResponseWriter (dove
// si scrive la risposta) e la *http.Request. "(a *api)" lo rende un metodo di
// api, così può usare a.db e a.log. Il nome "_" per la richiesta dice che
// non la usiamo.
func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	// TODO(BASE-2, passo B) — Implementare la liveness.
	//
	// Contratto: rispondi HTTP 200 con JSON {"status":"ok"}. Non interrogare
	// il database: questo endpoint dice solo che il processo è vivo.
	//
	// Usa writeJSON(w, http.StatusOK, ...) e una map[string]string.
	// Rimuovi la risposta 501 provvisoria qui sotto.
	// Fatto quando: make test-acceptance RUN=TestHealthz.
	writeError(w, http.StatusNotImplemented, "not_implemented", "TODO(BASE-2): healthz")
}

// readyz è la readiness: il servizio può gestire traffico (DB raggiungibile).
func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	// TODO(BASE-2, passo C) — Implementare la readiness.
	//
	// Contratto:
	//   - DB raggiungibile     → 200, {"status":"ready"};
	//   - DB non raggiungibile → log warning e 503 con codice "not_ready".
	//
	// Flusso:
	//   1. crea da r.Context() un context con timeout readyTimeout;
	//   2. usa defer cancel() per liberare il timer;
	//   3. chiama a.db.Ping(ctx);
	//   4. se Ping fallisce, registra "readiness check failed", scrivi
	//      writeError(..., http.StatusServiceUnavailable, "not_ready",
	//      "database not reachable") e fai return;
	//   5. altrimenti usa writeJSON per la risposta 200.
	//
	// Aggiungi "context" agli import. Rimuovi la risposta 501 provvisoria.
	// Fatto quando: make test-acceptance RUN=TestReadyz.
	writeError(w, http.StatusNotImplemented, "not_implemented", "TODO(BASE-2): readyz")
}
