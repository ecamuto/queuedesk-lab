package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"queuedesk/internal/auth"
	"queuedesk/internal/ticket"
)

// errorBody ed errorDetail descrivono il JSON di errore:
//
//	{"error":{"code":"not_ready","message":"database not reachable"}}
//
// Go: il testo tra backtick dopo un campo è un "tag". encoding/json lo legge
// per sapere il nome della chiave JSON (senza tag userebbe "Error", con la
// maiuscola). I campi devono essere esportati, altrimenti json li ignora.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// statusClientClosedRequest (499) non è uno status HTTP standard: è la
// convenzione di nginx per "il client ha chiuso la connessione prima della
// risposta". Il client non lo riceve mai; compare solo nei log.
const statusClientClosedRequest = 499

// writeError scrive l'envelope {"error":{"code":"...","message":"..."}}.
//
// L'ordine conta: prima gli header, poi WriteHeader(status), poi il body.
// Dopo il primo Write gli header non si possono più cambiare.
func writeError(w http.ResponseWriter, status int, code, msg string) {
	// Go: qui l'errore di Marshal è scartato con _ perché una struct di sole
	// stringhe non può fallire. È l'unico caso in cui ignorare un errore è
	// accettabile: quando sai dimostrare che non può succedere.
	data, _ := json.Marshal(errorBody{Error: errorDetail{Code: code, Message: msg}})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

// respondErr traduce un errore di dominio nella risposta HTTP. È l'unico
// punto in cui si decide quale status corrisponde a quale errore.
//
// Go: "switch {" senza espressione equivale a una catena di if/else if: vince
// il primo case vero. errors.Is(err, X) risponde "X è nella catena di err?",
// anche se err è stato avvolto con %w. errors.As(err, &target) cerca nella
// catena un errore del tipo di target e, se lo trova, lo copia lì.
func (a *api) respondErr(w http.ResponseWriter, r *http.Request, err error) {
	var reqErr *requestError
	switch {
	case errors.As(err, &reqErr):
		writeError(w, reqErr.status, reqErr.code, reqErr.msg)

	// 400: i messaggi di validazione sono scritti dai service e sicuri da mostrare.
	case errors.Is(err, auth.ErrInvalidInput), errors.Is(err, ticket.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())

	// 401
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
	case errors.Is(err, auth.ErrUnauthenticated):
		a.unauthorized(w)

	// TODO(CLAIM-4) — Tradurre gli errori del claim in status HTTP.
	//
	// Dove serve: claimTicket e changeStatus passano qui ogni errore di Claim
	// e ChangeStatus. Oggi finiscono nel default: 500 internal_error e una
	// riga ERROR nel log.
	//
	// Aggiungi quattro case a questo switch, qui fra il 401 e il 404:
	//   ticket.ErrForbidden         → 403, codice "forbidden", messaggio
	//                                 "this operation requires the agent role"
	//   ticket.ErrNotAssignee       → 403, codice "not_assignee", messaggio
	//                                 "only the assigned agent can change the status"
	//   ticket.ErrAlreadyClaimed    → 409, codice "already_claimed", messaggio
	//                                 "ticket already claimed by another agent"
	//   ticket.ErrInvalidTransition → 409, codice "invalid_transition", con
	//                                 err.Error() come messaggio: dice da quale a
	//                                 quale stato ("... open -> resolved") ed è
	//                                 scritto da noi, quindi sicuro da mostrare
	// Ogni case ha la forma: case errors.Is(err, ticket.ErrX): writeError(...).
	//
	// Usa errors.Is, non ==: gli errori arrivano avvolti da store e service
	// ("claim ticket 7: ticket already claimed") e == confronta solo lo strato
	// esterno. 403 = sei autenticato ma non puoi farlo; 409 = la richiesta è
	// valida, ma lo stato attuale del ticket la impedisce.
	// Fatto quando: make test-acceptance RUN=TestClaimErrorMapping.

	// 404
	case errors.Is(err, ticket.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "ticket not found")

	// 409
	case errors.Is(err, auth.ErrEmailTaken):
		writeError(w, http.StatusConflict, "email_taken", "email already registered")

	// 501: le parti ancora da implementare (i TODO restituiscono
	// errors.ErrUnsupported avvolto con l'ID del TODO).
	case errors.Is(err, errors.ErrUnsupported):
		writeError(w, http.StatusNotImplemented, "not_implemented", err.Error())

	// Il client ha chiuso la connessione: nessuno leggerà la risposta. Lo
	// status serve solo al log di accesso, che altrimenti registrerebbe un
	// 200 fuorviante (vedi statusClientClosedRequest).
	case errors.Is(err, context.Canceled) && r.Context().Err() != nil:
		a.log.DebugContext(r.Context(), "request cancelled by client")
		w.WriteHeader(statusClientClosedRequest)

	default:
		a.serverError(w, r, err)
	}
}

// serverError registra il dettaglio nel log e restituisce al client un
// messaggio generico: niente SQL, stack o dettagli interni nella risposta.
func (a *api) serverError(w http.ResponseWriter, r *http.Request, err error) {
	a.log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}
