package auth_test

import (
	"errors"
	"strings"
	"testing"

	"queuedesk/internal/auth"
)

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := auth.HashPassword("correct-horse")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "correct-horse" || !strings.HasPrefix(hash, "$2") {
		t.Fatalf("hash does not look like bcrypt: %q", hash)
	}

	tests := []struct {
		name     string
		hash     string
		password string
		want     error // nil = ok; per "corrupted" basta che non sia nil
	}{
		{"correct password", hash, "correct-horse", nil},
		{"wrong password", hash, "wrong-horse", auth.ErrInvalidCredentials},
		{"too long password", hash, strings.Repeat("x", 100), auth.ErrInvalidCredentials},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Go: errors.Is(nil, nil) è true: lo stesso confronto copre sia i casi
			// di successo (want nil) sia quelli di errore.
			if err := auth.CheckPassword(tt.hash, tt.password); !errors.Is(err, tt.want) {
				t.Fatalf("CheckPassword = %v, want %v", err, tt.want)
			}
		})
	}

	t.Run("corrupted hash is an internal error", func(t *testing.T) {
		err := auth.CheckPassword("not-a-bcrypt-hash", "correct-horse")
		if err == nil || errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("CheckPassword = %v, want a non-credential error", err)
		}
	})
}

func TestHashPasswordUsesSalt(t *testing.T) {
	a, err := auth.HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two hashes of the same password are equal: salt missing?")
	}
}
