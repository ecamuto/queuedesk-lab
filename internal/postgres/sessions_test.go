//go:build acceptance

// Acceptance test della tappa corrente: girano con `make test-acceptance`.
// Falliscono finché i TODO della tappa non sono completati (vedi TAPPA.md).

package postgres_test

import (
	"errors"
	"testing"
	"time"

	"queuedesk/internal/auth"
	"queuedesk/internal/postgres"
)

func TestSessionRepoExpiryAndRevocation(t *testing.T) {
	pool := testDB(t)
	u := mustUser(t, pool, "ada@example.com", "agent")
	repo := postgres.NewSessionRepo(pool)
	ctx := t.Context()

	_, hash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	// Go: time.Time ha precisione al nanosecondo, PostgreSQL al microsecondo.
	// Senza Truncate l'istante riletto dal DB non sarebbe identico a now.
	now := time.Now().UTC().Truncate(time.Microsecond) // precisione di TIMESTAMPTZ
	if err := repo.CreateSession(ctx, auth.Session{TokenHash: hash, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	p, err := repo.PrincipalBySession(ctx, hash, now)
	if err != nil {
		t.Fatalf("PrincipalBySession: %v", err)
	}
	if p.UserID != u.ID || p.Role != auth.RoleAgent || p.Email != "ada@example.com" {
		t.Fatalf("unexpected principal %+v", p)
	}

	for _, at := range []time.Time{now.Add(time.Hour), now.Add(2 * time.Hour)} {
		if _, err := repo.PrincipalBySession(ctx, hash, at); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("PrincipalBySession at %v = %v, want ErrNotFound (expired)", at, err)
		}
	}

	if err := repo.DeleteSession(ctx, hash); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := repo.PrincipalBySession(ctx, hash, now); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("PrincipalBySession after delete = %v, want ErrNotFound", err)
	}
	if err := repo.DeleteSession(ctx, hash); err != nil {
		t.Fatalf("second DeleteSession must be idempotent: %v", err)
	}
}

func TestSessionsAreDeletedWithTheirUser(t *testing.T) {
	pool := testDB(t)
	u := mustUser(t, pool, "ada@example.com", "requester")
	repo := postgres.NewSessionRepo(pool)
	ctx := t.Context()

	// Go: qui l'errore è ignorato con _: crypto/rand non fallisce in pratica,
	// e il test controlla altro. Nel codice di produzione non si fa.
	_, hash, _ := auth.NewToken()
	now := time.Now().UTC()
	if err := repo.CreateSession(ctx, auth.Session{TokenHash: hash, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d sessions survived their user (want ON DELETE CASCADE)", n)
	}
}

// Flusso completo auth.Service + repository reali.
//
// Go: lo stesso auth.Service degli unit test, ma con i repository veri al
// posto dei fake: le interfacce permettono entrambe le combinazioni.
func TestAuthServiceWithPostgres(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	svc := auth.NewService(postgres.NewUserRepo(pool), postgres.NewSessionRepo(pool), time.Hour, nil)

	if _, err := svc.Register(ctx, "Grace@Example.com", "correct-horse"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res, err := svc.Login(ctx, "grace@example.com", "correct-horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	var stored int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token_hash = $1`, auth.HashToken(res.Token)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatal("session not stored under SHA-256(token)")
	}

	p, err := svc.Authenticate(ctx, res.Token)
	if err != nil || p.Role != auth.RoleRequester {
		t.Fatalf("Authenticate = %+v, %v", p, err)
	}
	if err := svc.Logout(ctx, res.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, res.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate after logout = %v, want ErrUnauthenticated", err)
	}
}
