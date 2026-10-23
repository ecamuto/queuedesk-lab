package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxBodyBytes limita il body delle richieste: i payload di QueueDesk sono
// piccoli e un limite evita di leggere in memoria body enormi.
const maxBodyBytes = 64 << 10 // 64 KiB (64 * 1024)

// requestError è un errore del client già tradotto in status e codice.
//
// Go: error è un'interfaccia con un solo metodo, Error() string. Qualunque
// tipo lo abbia è un errore: qui *requestError, grazie al metodo sotto.
type requestError struct {
	status int
	code   string
	msg    string
}

func (e *requestError) Error() string { return e.msg }

// badRequest crea un requestError con status 400.
//
// Go: restituisce il tipo interfaccia error, ma dentro c'è un *requestError:
// respondErr lo ritrova con errors.As.
func badRequest(code, format string, args ...any) error {
	return &requestError{status: http.StatusBadRequest, code: code, msg: fmt.Sprintf(format, args...)}
}

// decodeJSON legge un oggetto JSON nel tipo dst.
//
// Versione minima: accetta qualsiasi Content-Type, body di qualunque
// dimensione, campi sconosciuti e dati in coda. Verrà resa robusta nella
// tappa 6 (ROB-1).
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	_ = w
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return jsonError(err)
	}
	return nil
}

// jsonError traduce gli errori di encoding/json in messaggi utili al client.
func jsonError(err error) error {
	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		maxErr    *http.MaxBytesError
	)
	switch {
	case errors.As(err, &maxErr):
		return &requestError{
			status: http.StatusRequestEntityTooLarge,
			code:   "body_too_large",
			msg:    fmt.Sprintf("body must not exceed %d bytes", maxErr.Limit),
		}
	case errors.As(err, &syntaxErr):
		return badRequest("invalid_json", "malformed JSON at byte %d", syntaxErr.Offset)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return badRequest("invalid_json", "malformed JSON: unexpected end of body")
	case errors.As(err, &typeErr):
		return badRequest("invalid_json", "field %q must be of type %s", typeErr.Field, typeErr.Type)
	case errors.Is(err, io.EOF):
		return badRequest("invalid_json", "body must not be empty")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json non esporta un tipo per questo caso.
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return badRequest("unknown_field", "unknown field %s", field)
	default:
		return badRequest("invalid_json", "invalid JSON body")
	}
}

// writeJSON serializza v prima di scrivere l'header: se il marshal fallisce
// si può ancora rispondere 500 invece di inviare un body troncato.
//
// Go: v è di tipo any, cioè "qualunque valore": una struct, una mappa, una
// slice. json.Marshal usa la reflection per scoprire com'è fatto.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		// Qui non c'è un logger (writeJSON non è un metodo di api), e va bene
		// così: i nostri DTO hanno solo stringhe, numeri, date e puntatori,
		// che non fanno fallire il marshal. L'unico caso teorico è un
		// json.RawMessage non valido, ma il payload degli eventi arriva da
		// una colonna JSONB, che PostgreSQL accetta solo se è JSON valido.
		// Con tipi più complessi (canali, funzioni, NaN, MarshalJSON
		// personalizzati) questo errore andrebbe registrato.
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
