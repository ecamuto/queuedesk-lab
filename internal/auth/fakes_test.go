package auth_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"queuedesk/internal/auth"
)

// Go: un fake è un'implementazione finta di un'interfaccia, scritta a mano.
// auth.Service accetta qualunque UserStore, quindi nei test gli diamo questo
// invece di PostgreSQL: i test girano in millisecondi e senza database.
// fakeUsers è un UserStore in memoria: piccolo, senza logica superflua.
//
// Go: il mutex serve anche in un fake: i test con -race o con goroutine
// potrebbero chiamarlo in parallelo, e una map Go non è sicura per accessi
// concorrenti.
type fakeUsers struct {
	mu      sync.Mutex
	byEmail map[string]auth.Credentials
	nextID  int
}

func newFakeUsers() *fakeUsers {
	// Go: una map va creata (con make o un letterale {}) prima di scriverci:
	// scrivere in una map nil è un panic. Leggere da una map nil invece è lecito.
	return &fakeUsers{byEmail: map[string]auth.Credentials{}}
}

func (f *fakeUsers) CreateUser(_ context.Context, u auth.NewUser) (auth.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.ToLower(u.Email)
	// Go: "v, ok := m[k]": ok dice se la chiave c'è. Senza ok non si
	// distinguerebbe una chiave assente da un valore zero.
	if _, ok := f.byEmail[key]; ok {
		return auth.User{}, auth.ErrEmailTaken
	}
	f.nextID++
	user := auth.User{ID: fmt.Sprintf("user-%d", f.nextID), Email: u.Email, Role: u.Role, CreatedAt: time.Now()}
	f.byEmail[key] = auth.Credentials{User: user, PasswordHash: u.PasswordHash}
	return user, nil
}

func (f *fakeUsers) CredentialsByEmail(_ context.Context, email string) (auth.Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byEmail[strings.ToLower(email)]
	if !ok {
		return auth.Credentials{}, auth.ErrNotFound
	}
	return c, nil
}

func (f *fakeUsers) byID(id string) (auth.User, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.byEmail {
		if c.ID == id {
			return c.User, true
		}
	}
	return auth.User{}, false
}

// seed inserisce un utente con hash bcrypt a costo minimo, per test veloci.
func (f *fakeUsers) seed(t *testing.T, email, password string, role auth.Role) auth.User {
	t.Helper()
	// MinCost invece di DefaultCost: stesso formato, ma molto più veloce.
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	// Go: t.Context() (Go 1.24+) è un context che viene cancellato alla fine
	// del test: il posto giusto per le chiamate fatte dai test.
	u, err := f.CreateUser(t.Context(), auth.NewUser{Email: email, PasswordHash: string(h), Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// fakeSessions è un SessionStore in memoria che rispetta la scadenza.
//
// Go: un []byte non può essere chiave di una map (le slice non sono
// confrontabili con ==): byHash usa string(hash) come chiave.
type fakeSessions struct {
	mu     sync.Mutex
	users  *fakeUsers
	byHash map[string]auth.Session
	err    error // se valorizzato, ogni metodo lo restituisce
}

func newFakeSessions(users *fakeUsers) *fakeSessions {
	return &fakeSessions{users: users, byHash: map[string]auth.Session{}}
}

func (f *fakeSessions) CreateSession(_ context.Context, s auth.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.byHash[string(s.TokenHash)] = s
	return nil
}

func (f *fakeSessions) PrincipalBySession(_ context.Context, hash []byte, now time.Time) (auth.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return auth.Principal{}, f.err
	}
	s, ok := f.byHash[string(hash)]
	if !ok || !s.ExpiresAt.After(now) {
		return auth.Principal{}, auth.ErrNotFound
	}
	u, ok := f.users.byID(s.UserID)
	if !ok {
		return auth.Principal{}, auth.ErrNotFound
	}
	return auth.Principal{UserID: u.ID, Email: u.Email, Role: u.Role}, nil
}

func (f *fakeSessions) DeleteSession(_ context.Context, hash []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	delete(f.byHash, string(hash))
	return nil
}

func (f *fakeSessions) all() []auth.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]auth.Session, 0, len(f.byHash))
	for _, s := range f.byHash {
		out = append(out, s)
	}
	return out
}

// fakeClock permette di far avanzare il tempo senza sleep.
//
// Go: c.Now è un method value con la firma func() time.Time: si passa a
// auth.NewService al posto di time.Now (vedi newFixture in session_test.go).
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
}
