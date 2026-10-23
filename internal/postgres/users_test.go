package postgres_test

import (
	"errors"
	"testing"

	"queuedesk/internal/auth"
	"queuedesk/internal/postgres"
)

func TestUserRepo(t *testing.T) {
	pool := testDB(t)
	repo := postgres.NewUserRepo(pool)
	ctx := t.Context()

	u, err := repo.CreateUser(ctx, auth.NewUser{Email: "Ada@Example.com", PasswordHash: "hash", Role: auth.RoleRequester})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Go: IsZero dice se un time.Time è il valore zero (mai impostato).
	// 36 caratteri è la forma testuale di un UUID.
	if len(u.ID) != 36 || u.Role != auth.RoleRequester || u.CreatedAt.IsZero() {
		t.Fatalf("unexpected user %+v", u)
	}

	t.Run("duplicate email is case-insensitive", func(t *testing.T) {
		_, err := repo.CreateUser(ctx, auth.NewUser{Email: "ada@example.COM", PasswordHash: "x", Role: auth.RoleRequester})
		if !errors.Is(err, auth.ErrEmailTaken) {
			t.Fatalf("CreateUser = %v, want ErrEmailTaken", err)
		}
	})

	t.Run("lookup is case-insensitive", func(t *testing.T) {
		c, err := repo.CredentialsByEmail(ctx, "ADA@EXAMPLE.COM")
		if err != nil {
			t.Fatalf("CredentialsByEmail: %v", err)
		}
		if c.ID != u.ID || c.PasswordHash != "hash" {
			t.Fatalf("unexpected credentials %+v", c)
		}
	})

	t.Run("unknown email", func(t *testing.T) {
		if _, err := repo.CredentialsByEmail(ctx, "nobody@example.com"); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("CredentialsByEmail = %v, want ErrNotFound", err)
		}
	})

	t.Run("role is constrained by the schema", func(t *testing.T) {
		// Go: "admin" è una costante non tipizzata e diventa un auth.Role senza
		// conversione: il compilatore non può impedirlo, il CHECK dello schema sì.
		_, err := repo.CreateUser(ctx, auth.NewUser{Email: "eve@example.com", PasswordHash: "x", Role: "admin"})
		if code := pgCode(err); code != "23514" {
			t.Fatalf("CreateUser with role admin = %v (SQLSTATE %q), want check_violation 23514", err, code)
		}
	})
}
