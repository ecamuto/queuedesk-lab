package httpapi

import "net/http"

type changeStatusRequest struct {
	Status string `json:"status"`
}

// claimTicket: POST /v1/tickets/{id}/claim. Nessun body: basta l'ID.
//
// Go: a.tickets.Claim ha la stessa firma di a.tickets.Get, quindi riusa
// withTicket (ticket_handlers.go). Una funzione con la firma giusta è
// intercambiabile con un'altra: è questo che rende utile passarla come valore.
func (a *api) claimTicket(w http.ResponseWriter, r *http.Request) {
	a.withTicket(w, r, a.tickets.Claim)
}

// changeStatus: PATCH /v1/tickets/{id}/status con body {"status":"..."}.
// Non usa withTicket perché, oltre all'ID, legge anche un body.
func (a *api) changeStatus(w http.ResponseWriter, r *http.Request) {
	actor, err := a.actor(r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	// Prima l'ID dal path, poi il body: un ID non valido risponde 400 senza
	// leggere il body.
	id, err := ticketID(r)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	var req changeStatusRequest
	if err := decodeJSON(w, r, &req); err != nil {
		a.respondErr(w, r, err)
		return
	}
	// La validazione dello stato ("closed" non esiste) la fa il service.
	t, err := a.tickets.ChangeStatus(r.Context(), actor, id, req.Status)
	if err != nil {
		a.respondErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toTicketResponse(t))
}
