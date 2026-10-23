package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"queuedesk/internal/auth"
)

func TestRegister(t *testing.T) {
	tests := []struct {
		name      string
		email     string
		password  string
		wantErr   error
		wantEmail string
	}{
		{"valid, email normalized", "  Ada@Example.COM ", "correct-horse", nil, "ada@example.com"},
		{"empty email", "", "correct-horse", auth.ErrInvalidInput, ""},
		{"not an email", "ada.example.com", "correct-horse", auth.ErrInvalidInput, ""},
		{"display name rejected", "Ada <ada@example.com>", "correct-horse", auth.ErrInvalidInput, ""},
		{"email too long", strings.Repeat("a", 250) + "@x.io", "correct-horse", auth.ErrInvalidInput, ""},
		{"password too short", "ada@example.com", "short", auth.ErrInvalidInput, ""},
		{"password too long", "ada@example.com", strings.Repeat("x", auth.MaxPasswordLen+1), auth.ErrInvalidInput, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := newFakeUsers()
			svc := auth.NewService(users, newFakeSessions(users), time.Hour, nil)

			u, err := svc.Register(t.Context(), tt.email, tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Register error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if u.Email != tt.wantEmail {
				t.Errorf("email = %q, want %q", u.Email, tt.wantEmail)
			}
			if u.Role != auth.RoleRequester {
				t.Errorf("role = %q, want requester", u.Role)
			}
		})
	}
}

func TestRegisterDuplicateEmailIsCaseInsensitive(t *testing.T) {
	users := newFakeUsers()
	users.seed(t, "ada@example.com", "correct-horse", auth.RoleRequester)
	svc := auth.NewService(users, newFakeSessions(users), time.Hour, nil)

	_, err := svc.Register(t.Context(), "ADA@example.com", "another-password")
	if !errors.Is(err, auth.ErrEmailTaken) {
		t.Fatalf("Register error = %v, want ErrEmailTaken", err)
	}
}

func TestRegisterStoresOnlyTheHash(t *testing.T) {
	users := newFakeUsers()
	svc := auth.NewService(users, newFakeSessions(users), time.Hour, nil)

	if _, err := svc.Register(t.Context(), "ada@example.com", "correct-horse"); err != nil {
		t.Fatal(err)
	}
	creds, err := users.CredentialsByEmail(t.Context(), "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if creds.PasswordHash == "correct-horse" {
		t.Fatal("password stored in clear text")
	}
	if err := auth.CheckPassword(creds.PasswordHash, "correct-horse"); err != nil {
		t.Fatalf("stored hash does not verify: %v", err)
	}
}
