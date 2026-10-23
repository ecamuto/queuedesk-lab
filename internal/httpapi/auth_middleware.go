package httpapi

import (
	"context"
	"net/http"

	"queuedesk/internal/auth"
)

// principalKey è la chiave del principal nel context. Stesso tipo ctxKey di
// requestIDKey (middleware.go) ma valore diverso: le due chiavi non si
// sovrascrivono.
const principalKey ctxKey = 1

// withPrincipal e principalFrom sono l'unico accesso al principal nel
// context: il resto del codice non usa mai ctx.Value direttamente.
func withPrincipal(ctx context.Context, p auth.Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// Go: type assertion nella forma con due risultati: ok è false se il valore
// manca o non è un auth.Principal, e in quel caso p è il valore zero.
func principalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalKey).(auth.Principal)
	return p, ok
}

// requireAuth autentica la richiesta tramite "Authorization: Bearer <token>".
// Assente, malformato, sconosciuto, scaduto o revocato → 401. Il controllo
// del ruolo (403) spetta al service, che conosce le regole di dominio.
//
// Go: stessa forma dei middleware di middleware.go, ma si applica a una
// rotta sola (in NewHandler) invece che a tutto il mux. Riceve un
// http.HandlerFunc, così in server.go si scrive a.requireAuth(a.me) senza
// conversioni.
func (a *api) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO(AUTH-4, passo B) — Proteggere la rotta con l'autenticazione.
		//
		// Dove serve: NewHandler (server.go) avvolge con requireAuth solo
		// GET /v1/me e DELETE /v1/auth/session; register, login e health
		// restano pubblici. Questa funzione gira una volta per richiesta,
		// prima dell'handler. next è un http.HandlerFunc: si chiama next(w, r).
		//
		// Quattro esiti, ognuno con una sola risposta:
		//   1. token, ok := bearerToken(r); !ok → a.unauthorized(w), return;
		//   2. p, err := a.auth.Authenticate(r.Context(), token);
		//      errors.Is(err, auth.ErrUnauthenticated) → a.unauthorized(w),
		//      return;
		//   3. qualsiasi altro errore → a.serverError(w, r, err), return. È un
		//      500: il guasto è del server, e serverError ne logga il dettaglio;
		//   4. successo: ctx := withPrincipal(r.Context(), p), poi
		//      next(w, r.WithContext(ctx)). Passa la copia, non r: l'handler me
		//      legge il principal con principalFrom.
		//
		// Senza return dopo un errore il codice prosegue e scrive una seconda
		// risposta (nel log del server: "superfluous response.WriteHeader").
		// Aggiungi "errors" agli import. Non loggare mai il token.
		// Rimuovi `_ = next` e la risposta 501 provvisoria.
		// Fatto quando: make test-acceptance RUN='TestRequireAuth|TestMe|TestLogout'.
		_ = next
		writeError(w, http.StatusNotImplemented, "not_implemented", "TODO(AUTH-4): requireAuth")
	})
}

// maxTokenLen scarta subito header assurdi senza interrogare il database.
const maxTokenLen = 128

// bearerToken estrae il token da "Authorization: Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	// TODO(AUTH-4, passo A) — Estrarre la credenziale Bearer.
	//
	// Dove serve: requireAuth (passo B), e l'handler logout, che la richiama
	// per avere il token da revocare. Controlla solo il formato dell'header:
	// non chiamare il service da qui.
	//
	//   1. header := r.Header.Get("Authorization"): "" se manca;
	//   2. scheme, token, ok := strings.Cut(header, " "): divide al primo
	//      spazio; ok è false se lo spazio non c'è (header vuoto o "Bearer");
	//   3. !ok, oppure schema diverso da "Bearer" → return "", false.
	//      Confronta con strings.EqualFold: lo schema HTTP non distingue le
	//      maiuscole, quindi "bearer" è valido; "Basic ..." no;
	//   4. token = strings.TrimSpace(token): toglie gli spazi in più;
	//   5. return token, true solo se token != "" e len(token) <= maxTokenLen
	//      (il token vero ha 43 caratteri: un header enorme non arriva al DB).
	//      In tutti gli altri casi ok è false.
	//
	// Aggiungi "strings" agli import.
	// Fatto quando: insieme al passo B, i casi "no header", "wrong scheme" ed
	// "empty bearer" di TestRequireAuth ricevono 401 senza chiamare il service.
	return "", false
}

// unauthorized risponde 401 con l'header WWW-Authenticate richiesto dallo
// standard HTTP: dice al client quale schema di autenticazione usare.
func (a *api) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="queuedesk"`)
	writeError(w, http.StatusUnauthorized, "unauthenticated", "missing, invalid or expired bearer token")
}
