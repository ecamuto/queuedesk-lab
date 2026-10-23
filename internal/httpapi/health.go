package httpapi

import (
	"context"
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
	// Go: map[string]string{"status": "ok"} è una mappa letterale;
	// writeJSON la serializza in {"status":"ok"}.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz è la readiness: il servizio può gestire traffico (DB raggiungibile).
func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	// Go: context.WithTimeout crea un context figlio che scade dopo 2s e
	// restituisce anche cancel, una funzione che lo libera prima. "defer"
	// rimanda la chiamata a quando readyz termina, qualunque return si
	// prenda: è il modo Go di non dimenticare la pulizia.
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()

	// a.db è un Pinger (interfaccia in server.go): qui non sappiamo, e non
	// ci interessa, che dietro c'è *pgxpool.Pool. Nei test è fakePinger.
	if err := a.db.Ping(ctx); err != nil {
		// Go: i log strutturati di slog prendono coppie chiave, valore.
		a.log.WarnContext(r.Context(), "readiness check failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "not_ready", "database not reachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
