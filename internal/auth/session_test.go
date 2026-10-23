//go:build acceptance

// Acceptance test della tappa corrente: girano con `make test-acceptance`.
// Falliscono finché i TODO della tappa non sono completati (vedi TAPPA.md).

package auth_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"queuedesk/internal/auth"
)

const ttl = 2 * time.Hour

// Go: una "fixture" raccoglie in una struct tutto ciò che serve a un test.
// Ogni test chiama newFixture e riceve dati nuovi: nessuno stato condiviso
// fra un test e l'altro.
type fixture struct {
	users    *fakeUsers
	sessions *fakeSessions
	clock    *fakeClock
	svc      *auth.Service
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	users := newFakeUsers()
	users.seed(t, "ada@example.com", "correct-horse", auth.RoleRequester)
	users.seed(t, "grace@example.com", "agent-password", auth.RoleAgent)
	sessions := newFakeSessions(users)
	clock := newClock()
	// Go: un composite literal senza nomi dei campi segue l'ordine della struct.
	// È comodo per struct piccole e private; altrove meglio i nomi (Campo: valore).
	return fixture{users, sessions, clock, auth.NewService(users, sessions, ttl, clock.Now)}
}

func TestLoginRejectsBadCredentialsGenerically(t *testing.T) {
	tests := []struct {
		name, email, password string
	}{
		{"wrong password", "ada@example.com", "wrong-horse"},
		{"unknown email", "nobody@example.com", "correct-horse"},
		{"malformed email", "not-an-email", "correct-horse"},
		{"empty password", "ada@example.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.svc.Login(t.Context(), tt.email, tt.password)
			// Deve essere esattamente l'errore generico, non un wrapping che
			// riveli il motivo.
			// Go: qui == e non errors.Is, di proposito: errors.Is accetterebbe anche
			// un errore avvolto ("login: user not found: ..."), che rivelerebbe il motivo.
			if err != auth.ErrInvalidCredentials {
				t.Fatalf("Login error = %v, want exactly ErrInvalidCredentials", err)
			}
			if n := len(f.sessions.all()); n != 0 {
				t.Fatalf("%d sessions created after a failed login", n)
			}
		})
	}
}

func TestLoginCreatesSessionWithHashOnly(t *testing.T) {
	f := newFixture(t)

	res, err := f.svc.Login(t.Context(), "ADA@example.com", "correct-horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.Token == "" {
		t.Fatal("empty token")
	}
	// Go: i time.Time si confrontano con Equal, non con ==: == guarda anche
	// il fuso e la lettura monotona, e due istanti uguali potrebbero risultare diversi.
	if want := f.clock.Now().Add(ttl); !res.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", res.ExpiresAt, want)
	}

	all := f.sessions.all()
	if len(all) != 1 {
		t.Fatalf("got %d sessions, want 1", len(all))
	}
	s := all[0]
	// Go: le slice non si confrontano con ==: per []byte c'è bytes.Equal.
	if !bytes.Equal(s.TokenHash, auth.HashToken(res.Token)) {
		t.Fatal("stored hash is not SHA-256(token)")
	}
	if bytes.Contains(s.TokenHash, []byte(res.Token)) {
		t.Fatal("token stored in clear text")
	}
	if !s.CreatedAt.Equal(f.clock.Now()) || !s.ExpiresAt.Equal(res.ExpiresAt) {
		t.Fatalf("unexpected session timestamps: %+v", s)
	}
}

func TestLoginStoreFailureIsNotACredentialError(t *testing.T) {
	f := newFixture(t)
	f.sessions.err = errors.New("db down")

	_, err := f.svc.Login(t.Context(), "ada@example.com", "correct-horse")
	if err == nil || errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("Login error = %v, want an internal error", err)
	}
}

// Go: la tabella ha un campo di tipo funzione (token): ogni caso decide
// quale token usare a partire da quello valido, creato solo dentro il test.
func TestAuthenticate(t *testing.T) {
	tests := []struct {
		name     string
		token    func(valid string) string // sceglie il token da usare
		advance  time.Duration             // avanzamento dell'orologio
		wantErr  error
		wantRole auth.Role
	}{
		{"valid token", func(v string) string { return v }, 0, nil, auth.RoleAgent},
		{"valid just before expiry", func(v string) string { return v }, ttl - time.Second, nil, auth.RoleAgent},
		{"expired exactly at expires_at", func(v string) string { return v }, ttl, auth.ErrUnauthenticated, ""},
		{"expired", func(v string) string { return v }, ttl + time.Minute, auth.ErrUnauthenticated, ""},
		{"empty token", func(string) string { return "" }, 0, auth.ErrUnauthenticated, ""},
		{"unknown token", func(string) string { return "not-a-real-token" }, 0, auth.ErrUnauthenticated, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			res, err := f.svc.Login(t.Context(), "grace@example.com", "agent-password")
			if err != nil {
				t.Fatal(err)
			}
			f.clock.Advance(tt.advance)

			p, err := f.svc.Authenticate(t.Context(), tt.token(res.Token))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authenticate error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (p.Role != tt.wantRole || p.Email != "grace@example.com") {
				t.Fatalf("unexpected principal %+v", p)
			}
		})
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Login(t.Context(), "ada@example.com", "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(t.Context(), res.Token); err != nil {
		t.Fatalf("Authenticate before logout: %v", err)
	}

	if err := f.svc.Logout(t.Context(), res.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := f.svc.Authenticate(t.Context(), res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate after logout = %v, want ErrUnauthenticated", err)
	}
	// Idempotente: un secondo logout non è un errore.
	if err := f.svc.Logout(t.Context(), res.Token); err != nil {
		t.Fatalf("second Logout: %v", err)
	}
}

func TestAuthenticateStoreFailureIsInternal(t *testing.T) {
	f := newFixture(t)
	f.sessions.err = errors.New("db down")

	_, err := f.svc.Authenticate(t.Context(), "some-token")
	if err == nil || errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate error = %v, want an internal error (500, not 401)", err)
	}
}
