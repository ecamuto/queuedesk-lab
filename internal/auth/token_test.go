//go:build acceptance

// Acceptance test della tappa corrente: girano con `make test-acceptance`.
// Falliscono finché i TODO della tappa non sono completati (vedi TAPPA.md).

package auth_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"queuedesk/internal/auth"
)

func TestNewToken(t *testing.T) {
	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token is not base64.RawURLEncoding: %v", err)
	}
	if len(raw) != auth.TokenBytes {
		t.Fatalf("token carries %d random bytes, want %d", len(raw), auth.TokenBytes)
	}

	want := sha256.Sum256([]byte(token))
	if !bytes.Equal(hash, want[:]) {
		t.Fatal("hash is not SHA-256(token)")
	}
	if !bytes.Equal(auth.HashToken(token), hash) {
		t.Fatal("HashToken(token) differs from the hash returned by NewToken")
	}
}

func TestNewTokenIsUnique(t *testing.T) {
	// Go: map[string]bool usata come insieme: seen[token] vale false (il
	// valore zero) per le chiavi mai inserite.
	seen := make(map[string]bool)
	// Go: "for range 100" (Go 1.22+) ripete 100 volte senza variabile di ciclo.
	for range 100 {
		token, _, err := auth.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[token] {
			t.Fatalf("duplicate token %q", token)
		}
		seen[token] = true
	}
}
