package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

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
		token, ok := bearerToken(r)
		if !ok {
			a.unauthorized(w)
			return
		}
		p, err := a.auth.Authenticate(r.Context(), token)
		switch {
		case errors.Is(err, auth.ErrUnauthenticated):
			a.unauthorized(w)
			return
		case err != nil:
			a.serverError(w, r, err) // DB giù: 500, non 401
			return
		}
		// Go: next è una funzione: la si chiama direttamente con (w, r).
		// La richiesta passata è una copia con il principal nel context.
		next(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

// maxTokenLen scarta subito header assurdi senza interrogare il database.
const maxTokenLen = 128

// bearerToken estrae il token da "Authorization: Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	// Go: strings.Cut divide la stringa al primo separatore e restituisce
	// prima, dopo e se il separatore c'era. Più leggibile di Index + slicing.
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	// Go: EqualFold confronta ignorando maiuscole/minuscole: lo schema HTTP
	// è case-insensitive ("bearer" va bene quanto "Bearer").
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != "" && len(token) <= maxTokenLen
}

// unauthorized risponde 401 con l'header WWW-Authenticate richiesto dallo
// standard HTTP: dice al client quale schema di autenticazione usare.
func (a *api) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="queuedesk"`)
	writeError(w, http.StatusUnauthorized, "unauthenticated", "missing, invalid or expired bearer token")
}
