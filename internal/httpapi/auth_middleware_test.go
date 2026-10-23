//go:build acceptance

// Acceptance test della tappa corrente: girano con `make test-acceptance`.
// Falliscono finché i TODO della tappa non sono completati (vedi TAPPA.md).

package httpapi_test

import (
	"errors"
	"net/http"
	"testing"
)

func TestRequireAuth(t *testing.T) {
	tests := []struct {
		name    string
		opts    []reqOpt
		authErr error
		want    int
		code    string
	}{
		{"no header", nil, nil, http.StatusUnauthorized, "unauthenticated"},
		{"wrong scheme", []reqOpt{header("Authorization", "Basic YWRhOnB3")}, nil, http.StatusUnauthorized, "unauthenticated"},
		{"empty bearer", []reqOpt{header("Authorization", "Bearer ")}, nil, http.StatusUnauthorized, "unauthenticated"},
		{"unknown token", []reqOpt{bearer("forged")}, nil, http.StatusUnauthorized, "unauthenticated"},
		{"session store down is 500, not 401", []reqOpt{bearer(agentToken)}, errors.New("db down"), http.StatusInternalServerError, "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHandler(withAuth(&fakeAuth{authErr: tt.authErr}))
			// Go: tt.opts... espande la slice di opzioni negli argomenti variadici
			// di serve (nil va bene: zero opzioni).
			rec := serve(h, http.MethodGet, "/v1/me", "", tt.opts...)
			assertError(t, rec, tt.want, tt.code)
			if tt.want == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("401 without WWW-Authenticate header")
			}
		})
	}
}

func TestMe(t *testing.T) {
	h := newHandler(withAuth(&fakeAuth{}))
	// Go: range su una map dà chiave e valore, in ordine casuale a ogni
	// esecuzione: il test non deve dipendere dall'ordine.
	for token, want := range principals {
		rec := serve(h, http.MethodGet, "/v1/me", "", bearer(token))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
		}
		body := decodeBody[map[string]string](t, rec)
		if body["id"] != want.UserID || body["role"] != string(want.Role) || body["email"] != want.Email {
			t.Fatalf("body = %v, want %+v", body, want)
		}
	}
}

func TestLogout(t *testing.T) {
	fa := &fakeAuth{}
	h := newHandler(withAuth(fa))

	rec := serve(h, http.MethodDelete, "/v1/auth/session", "", bearer(requesterToken))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(fa.loggedOut) != 1 || fa.loggedOut[0] != requesterToken {
		t.Fatalf("logged out tokens = %v", fa.loggedOut)
	}
	assertError(t, serve(h, http.MethodDelete, "/v1/auth/session", ""), http.StatusUnauthorized, "unauthenticated")
}
