//go:build acceptance

// Acceptance test della tappa corrente: girano con `make test-acceptance`.
// Falliscono finché i TODO della tappa non sono completati (vedi TAPPA.md).

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestDecodeJSONHardening usa /v1/auth/register (rotta pubblica) per
// verificare le regole di decodeJSON indipendentemente dall'autenticazione.
func TestDecodeJSONHardening(t *testing.T) {
	h := newHandler(withAuth(&fakeAuth{}))
	valid := `{"email":"ada@example.com","password":"correct-horse"}`

	tests := []struct {
		name string
		body string
		ct   string // Content-Type; "-" = assente
		want int
		code string // "" = successo
	}{
		{"charset parameter accepted", valid, "application/json; charset=utf-8", http.StatusCreated, ""},
		{"missing content type", valid, "-", http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"text/plain", valid, "text/plain", http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"form encoded", "email=a&password=b", "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"unknown field", `{"email":"ada@example.com","password":"correct-horse","role":"agent"}`, "", http.StatusBadRequest, "unknown_field"},
		{"two JSON values", valid + valid, "", http.StatusBadRequest, "invalid_json"},
		{"trailing garbage", valid + " x", "", http.StatusBadRequest, "invalid_json"},
		// Go: 70<<10 = 70 KiB, oltre il limite di 64 KiB di maxBodyBytes.
		{"body too large", `{"email":"ada@example.com","password":"` + strings.Repeat("x", 70<<10) + `"}`, "", http.StatusRequestEntityTooLarge, "body_too_large"},
		{"array instead of object", `[]`, "", http.StatusBadRequest, "invalid_json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []reqOpt
			// Go: un case vuoto (case "":) è lecito e non fa nulla: in Go non c'è
			// fallthrough automatico verso il case successivo.
			switch tt.ct {
			case "":
			case "-":
				opts = append(opts, func(r *http.Request) { r.Header.Del("Content-Type") })
			default:
				opts = append(opts, header("Content-Type", tt.ct))
			}
			rec := serve(h, http.MethodPost, "/v1/auth/register", tt.body, opts...)
			if tt.code != "" {
				assertError(t, rec, tt.want, tt.code)
				return
			}
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body)
			}
		})
	}
}
