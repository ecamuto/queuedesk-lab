package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"queuedesk/internal/auth"
	"queuedesk/internal/httpapi"
)

// Go: "package httpapi_test" (con _test): i test usano httpapi solo
// attraverso ciò che esporta (NewHandler, Deps), come farebbe main. I file
// _test dello stesso package si vedono fra loro: fakeAuth è usato da tutti.

// Token "magici" riconosciuti da fakeAuth.
const (
	requesterToken = "requester-token"
	agentToken     = "agent-token"
)

// Go: nel letterale di una map di struct si può omettere il tipo del
// valore: {UserID: ...} invece di auth.Principal{UserID: ...}.
var principals = map[string]auth.Principal{
	requesterToken: {UserID: "req-1", Email: "ada@example.com", Role: auth.RoleRequester},
	agentToken:     {UserID: "agent-1", Email: "grace@example.com", Role: auth.RoleAgent},
}

// fakeAuth simula auth.Service con comportamenti fissi.
//
// Go: il comportamento dipende dall'input ("taken@example.com" → email già
// presa): ogni test sceglie lo scenario con i dati che invia, senza
// configurare il fake.
type fakeAuth struct {
	mu        sync.Mutex
	loggedOut []string
	authErr   error // se valorizzato, Authenticate lo restituisce
}

func (f *fakeAuth) Register(_ context.Context, email, _ string) (auth.User, error) {
	switch email {
	case "taken@example.com":
		return auth.User{}, fmt.Errorf("register: %w", auth.ErrEmailTaken)
	case "invalid":
		return auth.User{}, fmt.Errorf("%w: email is not valid", auth.ErrInvalidInput)
	case "panic@example.com":
		// Simula un bug: recoverPanics deve trasformarlo in un 500.
		panic("simulated bug")
	}
	return auth.User{ID: "new-user", Email: email, Role: auth.RoleRequester, CreatedAt: time.Now()}, nil
}

func (f *fakeAuth) Login(_ context.Context, email, password string) (auth.LoginResult, error) {
	if email == "ada@example.com" && password == "correct-horse" {
		return auth.LoginResult{Token: requesterToken, ExpiresAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}, nil
	}
	return auth.LoginResult{}, auth.ErrInvalidCredentials
}

func (f *fakeAuth) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	if f.authErr != nil {
		return auth.Principal{}, f.authErr
	}
	p, ok := principals[token]
	if !ok {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	return p, nil
}

func (f *fakeAuth) Logout(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loggedOut = append(f.loggedOut, token)
	return nil
}

// Go: withAuth restituisce una closure che ricorda fa. *fakeAuth soddisfa
// httpapi.AuthService (ha i quattro metodi), quindi si assegna a d.Auth.
func withAuth(fa *fakeAuth) option { return func(d *httpapi.Deps) { d.Auth = fa } }

func bearer(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}
