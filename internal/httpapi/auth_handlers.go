package httpapi

import (
	"errors"
	"net/http"
	"time"

	"queuedesk/internal/auth"
)

// I DTO separano il formato JSON dell'API dai tipi di dominio: rinominare
// un campo Go non cambia il contratto, e campi sensibili (password_hash)
// non possono finire in una risposta per sbaglio.
//
// Go: DTO = Data Transfer Object, struct che esistono solo per il JSON. I
// tag `json:"..."` fissano i nomi delle chiavi in snake_case; time.Time
// viene serializzato in RFC 3339 ("2026-10-20T15:04:05Z").

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      auth.Role `json:"role"` // Go: un tipo string con nome diventa una stringa JSON
	CreatedAt time.Time `json:"created_at"`
}

type loginResponse struct {
	Token     string    `json:"token"`
	TokenType string    `json:"token_type"`
	ExpiresAt time.Time `json:"expires_at"`
}

type meResponse struct {
	ID    string    `json:"id"`
	Email string    `json:"email"`
	Role  auth.Role `json:"role"`
}

// register: POST /v1/auth/register. Tutti gli handler seguono lo stesso
// schema: decodifica, chiama il service, traduce l'errore o scrive il JSON.
func (a *api) register(w http.ResponseWriter, r *http.Request) {
	// Go: "var req credentialsRequest" crea la struct al valore zero;
	// decodeJSON la riempie attraverso il puntatore &req.
	var req credentialsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		a.respondErr(w, r, err)
		return // Go: dopo aver scritto la risposta si esce, sempre
	}
	// r.Context() viene cancellato se il client chiude la connessione: il
	// service e il DB smettono di lavorare per nessuno.
	u, err := a.auth.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, userResponse{ID: u.ID, Email: u.Email, Role: u.Role, CreatedAt: u.CreatedAt.UTC()})
}

// login: POST /v1/auth/login. Restituisce il token in chiaro, una volta sola.
func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		a.respondErr(w, r, err)
		return
	}
	res, err := a.auth.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	// Il token non deve restare in cache di proxy o browser.
	// Go: gli header vanno impostati prima di WriteHeader (chiamato da
	// writeJSON): dopo verrebbero ignorati.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, loginResponse{Token: res.Token, TokenType: "Bearer", ExpiresAt: res.ExpiresAt.UTC()})
}

// logout: DELETE /v1/auth/session. 204 senza body.
func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	token, _ := bearerToken(r) // già validato da requireAuth
	if err := a.auth.Logout(r.Context(), token); err != nil {
		a.respondErr(w, r, err)
		return
	}
	// Go: WriteHeader senza Write produce una risposta con body vuoto.
	w.WriteHeader(http.StatusNoContent)
}

// me: GET /v1/me. Non interroga il database: il principal è già nel context.
func (a *api) me(w http.ResponseWriter, r *http.Request) {
	// Go: la forma "valore, ok" dice se il principal c'è davvero. Se manca,
	// la rotta non è stata avvolta da requireAuth: è un bug nostro, quindi
	// 500 e non 401.
	p, ok := principalFrom(r.Context())
	if !ok {
		a.serverError(w, r, errors.New("me: principal missing from context"))
		return
	}
	writeJSON(w, http.StatusOK, meResponse{ID: p.UserID, Email: p.Email, Role: p.Role})
}
