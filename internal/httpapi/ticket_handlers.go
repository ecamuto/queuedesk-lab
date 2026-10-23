package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"queuedesk/internal/auth"
	"queuedesk/internal/ticket"
)

type createTicketRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ticketResponse è il JSON di un ticket.
//
// Go: AssigneeID è un *string: un puntatore nil diventa null in JSON, quindi
// il client vede "assignee_id": null finché nessuno ha fatto claim.
type ticketResponse struct {
	ID          int64         `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Status      ticket.Status `json:"status"`
	ReporterID  string        `json:"reporter_id"`
	AssigneeID  *string       `json:"assignee_id"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// toTicketResponse converte il tipo di dominio nel DTO JSON.
func toTicketResponse(t ticket.Ticket) ticketResponse {
	return ticketResponse{
		ID:          t.ID,
		Title:       t.Title,
		Description: t.Description,
		Status:      t.Status,
		ReporterID:  t.ReporterID,
		AssigneeID:  t.AssigneeID,
		CreatedAt:   t.CreatedAt.UTC(),
		UpdatedAt:   t.UpdatedAt.UTC(),
	}
}

// ticketListResponse avvolge la lista in un oggetto ({"tickets":[...]})
// invece di restituire un array nudo: si potranno aggiungere campi (es. un
// cursore di paginazione) senza rompere i client.
type ticketListResponse struct {
	Tickets []ticketResponse `json:"tickets"`
}

// eventResponse è il JSON di un evento di audit.
//
// Go: json.Marshal copia un json.RawMessage così com'è, senza ricodificarlo:
// il payload compare come oggetto JSON, non come stringa.
type eventResponse struct {
	ID        int64            `json:"id"`
	TicketID  int64            `json:"ticket_id"`
	ActorID   string           `json:"actor_id"`
	Kind      ticket.EventKind `json:"kind"`
	Payload   json.RawMessage  `json:"payload"`
	CreatedAt time.Time        `json:"created_at"`
}

type eventListResponse struct {
	Events []eventResponse `json:"events"`
}

// toEventResponse converte un evento; un payload vuoto diventa {}.
func toEventResponse(e ticket.Event) eventResponse {
	payload := e.Payload
	// Go: len su una slice nil vale 0, quindi questo controllo copre sia nil
	// sia una slice vuota. Un RawMessage vuoto produrrebbe JSON non valido.
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return eventResponse{
		ID:        e.ID,
		TicketID:  e.TicketID,
		ActorID:   e.ActorID,
		Kind:      e.Kind,
		Payload:   payload,
		CreatedAt: e.CreatedAt.UTC(),
	}
}

// actor ricava l'attore dal principal messo nel context da requireAuth.
//
// Go: qui si traduce auth.Principal in ticket.Actor. È il layer HTTP a
// conoscere entrambi i package; ticket resta indipendente da auth.
func (a *api) actor(r *http.Request) (ticket.Actor, error) {
	p, ok := principalFrom(r.Context())
	if !ok {
		return ticket.Actor{}, errors.New("principal missing from context: route not wrapped by requireAuth?")
	}
	return ticket.Actor{UserID: p.UserID, IsAgent: p.Role == auth.RoleAgent}, nil
}

// ticketID legge {id} dal path.
//
// Go: r.PathValue("id") (Go 1.22+) restituisce il segmento che il ServeMux
// ha associato a {id} nel pattern "GET /v1/tickets/{id}". È sempre una
// string: la conversione in numero è compito nostro. ParseInt(raw, 10, 64)
// vuol dire base 10, risultato che sta in 64 bit.
func ticketID(r *http.Request) (int64, error) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, badRequest("invalid_id", "ticket id must be a positive integer, got %q", raw)
	}
	return id, nil
}

// createTicket: POST /v1/tickets. Risponde 201 con l'header Location.
func (a *api) createTicket(w http.ResponseWriter, r *http.Request) {
	actor, err := a.actor(r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	var req createTicketRequest
	if err := decodeJSON(w, r, &req); err != nil {
		a.respondErr(w, r, err)
		return
	}
	t, err := a.tickets.Create(r.Context(), actor, req.Title, req.Description)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	// Location dice al client dove trovare la risorsa appena creata.
	w.Header().Set("Location", fmt.Sprintf("/v1/tickets/%d", t.ID))
	writeJSON(w, http.StatusCreated, toTicketResponse(t))
}

// listTickets: GET /v1/tickets?status=open&limit=20.
func (a *api) listTickets(w http.ResponseWriter, r *http.Request) {
	actor, err := a.actor(r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	// Go: r.URL.Query() decodifica la query string in una url.Values (una
	// map[string][]string). Get restituisce il primo valore, "" se manca.
	q := r.URL.Query()
	ts, err := a.tickets.List(r.Context(), actor, ticket.ListQuery{Status: q.Get("status"), Limit: q.Get("limit")})
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	// Go: una slice nil diventa null in JSON, una slice vuota []. make con
	// lunghezza 0 e capacità len(ts) dà [] e prealloca lo spazio: gli append
	// del ciclo non devono riallocare.
	resp := ticketListResponse{Tickets: make([]ticketResponse, 0, len(ts))} // [] e non null
	for _, t := range ts {
		resp.Tickets = append(resp.Tickets, toTicketResponse(t))
	}
	writeJSON(w, http.StatusOK, resp)
}

// getTicket: GET /v1/tickets/{id}.
//
// Go: a.tickets.Get senza parentesi è un "method value": il metodo Get già
// legato ad a.tickets, passato come valore di tipo funzione.
func (a *api) getTicket(w http.ResponseWriter, r *http.Request) {
	a.withTicket(w, r, a.tickets.Get)
}

// withTicket fattorizza gli handler "leggi id, chiama il service, rispondi".
//
// Go: il parametro op ha un tipo funzione scritto per esteso. Qualunque
// funzione con questa firma va bene: Get e Claim del TicketService la hanno.
func (a *api) withTicket(w http.ResponseWriter, r *http.Request,
	op func(context.Context, ticket.Actor, int64) (ticket.Ticket, error)) {
	actor, err := a.actor(r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	id, err := ticketID(r)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	t, err := op(r.Context(), actor, id)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTicketResponse(t))
}

// ticketEvents: GET /v1/tickets/{id}/events.
func (a *api) ticketEvents(w http.ResponseWriter, r *http.Request) {
	actor, err := a.actor(r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	id, err := ticketID(r)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	evs, err := a.tickets.Events(r.Context(), actor, id)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	resp := eventListResponse{Events: make([]eventResponse, 0, len(evs))}
	for _, e := range evs {
		resp.Events = append(resp.Events, toEventResponse(e))
	}
	writeJSON(w, http.StatusOK, resp)
}
