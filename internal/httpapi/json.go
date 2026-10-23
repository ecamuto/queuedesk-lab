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

// decodeJSON legge esattamente un oggetto JSON nel tipo dst.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	// TODO(ROB-1) — Un body, un oggetto JSON: controllato prima di usarlo.
	//
	// Dove serve: ogni handler con un body (register, login, creazione del
	// ticket, cambio di stato) chiama decodeJSON. Questa versione accetta
	// qualunque Content-Type, ignora i campi sconosciuti (il "role":"agent"
	// nella register passa in silenzio), legge solo il primo valore e non ha
	// limiti di dimensione.
	//
	// Passo A — Content-Type, prima di leggere il body:
	//   mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"));
	//   se err != nil oppure mt != "application/json" → return
	//   &requestError{status: http.StatusUnsupportedMediaType,
	//   code: "unsupported_media_type", msg: "Content-Type must be application/json"}.
	//   Non badRequest, che darebbe 400. ParseMediaType separa i parametri:
	//   "application/json; charset=utf-8" → "application/json"; un header
	//   assente è una stringa vuota → err. Va fatto prima del Decode: un form
	//   (email=a&password=b) darebbe altrimenti 400 invalid_json, non 415.
	//
	// Passo B — Limite e decoder, in quest'ordine:
	//   r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	//   dec := json.NewDecoder(r.Body)
	//   dec.DisallowUnknownFields()
	//   Il decoder va creato dopo aver sostituito r.Body, altrimenti legge il
	//   body originale, senza limite. w serve a MaxBytesReader: superato il
	//   limite, chiede al server di chiudere la connessione.
	//
	// Passo C — Il primo valore: dec.Decode(dst); se err → return
	// jsonError(err). jsonError, già scritto qui sotto, traduce ogni caso:
	// sintassi, tipo sbagliato, body vuoto, campo sconosciuto (400
	// unknown_field), *http.MaxBytesError (413 body_too_large).
	//
	// Passo D — Nient'altro dopo il primo valore. Decode legge un valore e si
	// ferma: {…}{…} e {…} x passerebbero. Un secondo dec.Decode(&struct{}{})
	// deve restituire io.EOF (controlla con errors.Is):
	//   - se l'errore è un *http.MaxBytesError (errors.As) → return
	//     jsonError(err): il limite è stato superato dopo il primo oggetto, 413;
	//   - qualunque altro esito, nil compreso (c'era un secondo oggetto) →
	//     return badRequest("invalid_json", "body must contain a single JSON object").
	// dec.More() non basta: non vede un "}" o un "]" di troppo.
	//
	// Rimuovi "_ = w" e il Decode provvisorio; aggiungi "mime" agli import.
	// Fatto quando: make test-acceptance RUN=TestDecodeJSONHardening è verde
	// (7 subtest rossi → 0; "charset parameter accepted" e "array instead of
	// object" sono verdi già con lo stub). Dal vivo, make smoke passa da
	// "27 ok, 2 falliti" a "29 ok, 0 falliti".
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
