package httpapi_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestRegister(t *testing.T) {
	h := newHandler(withAuth(&fakeAuth{}))
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"created", `{"email":"ada@example.com","password":"correct-horse"}`, http.StatusCreated, ""},
		{"email taken", `{"email":"taken@example.com","password":"correct-horse"}`, http.StatusConflict, "email_taken"},
		{"invalid input", `{"email":"invalid","password":"correct-horse"}`, http.StatusBadRequest, "invalid_input"},
		{"malformed JSON", `{"email":`, http.StatusBadRequest, "invalid_json"},
		{"syntax error", `{"email" "x"}`, http.StatusBadRequest, "invalid_json"},
		{"wrong type", `{"email":123,"password":"x"}`, http.StatusBadRequest, "invalid_json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, http.MethodPost, "/v1/auth/register", tt.body)
			if tt.wantCode != "" {
				assertError(t, rec, tt.wantStatus, tt.wantCode)
				return
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantStatus, rec.Body)
			}
			// Go: map[string]any perché i valori JSON hanno tipi diversi. Un valore
			// any si confronta con == con una string ("requester"): vale true solo se
			// dentro c'è proprio quella string.
			body := decodeBody[map[string]any](t, rec)
			if body["role"] != "requester" || body["email"] != "ada@example.com" {
				t.Fatalf("unexpected body %v", body)
			}
			// Go: la forma "_, ok := m[k]" verifica solo la presenza della chiave.
			if _, leaked := body["password_hash"]; leaked {
				t.Fatal("password_hash exposed in the response")
			}
		})
	}
}

func TestLogin(t *testing.T) {
	h := newHandler(withAuth(&fakeAuth{}))

	// Go: t.Run anche senza tabella: raggruppa scenari dello stesso test, e
	// ognuno compare con il suo nome nell'output (TestLogin/success).
	t.Run("success", func(t *testing.T) {
		rec := serve(h, http.MethodPost, "/v1/auth/login", `{"email":"ada@example.com","password":"correct-horse"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("Cache-Control = %q, want no-store", cc)
		}
		body := decodeBody[map[string]string](t, rec)
		if body["token"] != requesterToken || body["token_type"] != "Bearer" || body["expires_at"] == "" {
			t.Fatalf("unexpected body %v", body)
		}
	})

	t.Run("wrong credentials are indistinguishable", func(t *testing.T) {
		wrongPwd := serve(h, http.MethodPost, "/v1/auth/login", `{"email":"ada@example.com","password":"nope"}`)
		unknown := serve(h, http.MethodPost, "/v1/auth/login", `{"email":"ghost@example.com","password":"correct-horse"}`)
		assertError(t, wrongPwd, http.StatusUnauthorized, "invalid_credentials")
		assertError(t, unknown, http.StatusUnauthorized, "invalid_credentials")
		if wrongPwd.Body.String() != unknown.Body.String() {
			t.Fatalf("responses differ:\n%s\n%s", wrongPwd.Body, unknown.Body)
		}
	})
}

func TestPanicBecomes500(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := newHandler(withAuth(&fakeAuth{}), withLogger(log))
	rec := serve(h, http.MethodPost, "/v1/auth/register", `{"email":"panic@example.com","password":"correct-horse"}`)
	assertError(t, rec, http.StatusInternalServerError, "internal_error")
	if strings.Contains(rec.Body.String(), "simulated bug") {
		t.Fatal("panic details leaked to the client")
	}

	// Anche la richiesta andata in panic ha la sua riga di access log.
	// Go: bufio.Scanner legge un testo riga per riga; ogni riga è un record
	// JSON di slog (il panic ne scrive uno, il log di accesso un altro).
	sc := bufio.NewScanner(&buf)
	sc.Buffer(nil, 1<<20) // lo stack del panic può superare i 64 KiB di default
	for sc.Scan() {
		var line struct {
			Msg    string `json:"msg"`
			Status int    `json:"status"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Msg == "http request" {
			if line.Status != http.StatusInternalServerError {
				t.Fatalf("access log status = %d, want 500", line.Status)
			}
			return
		}
	}
	t.Fatalf("no access log line for the panicking request:\n%s", buf.String())
}

func TestEmptyBodyIsRejected(t *testing.T) {
	h := newHandler(withAuth(&fakeAuth{}))
	rec := serve(h, http.MethodPost, "/v1/auth/register", "", header("Content-Type", "application/json"))
	assertError(t, rec, http.StatusBadRequest, "invalid_json")
}
