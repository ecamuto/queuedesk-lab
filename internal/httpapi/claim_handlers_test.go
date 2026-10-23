//go:build acceptance

// Acceptance test della tappa corrente: girano con `make test-acceptance`.
// Falliscono finché i TODO della tappa non sono completati (vedi TAPPA.md).

package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"queuedesk/internal/ticket"
)

func TestClaimErrorMapping(t *testing.T) {
	// Go: i campi di fakeTickets sono funzioni: ogni test scrive solo quelle
	// che gli servono, come funzioni anonime. Qui l'ID della richiesta sceglie
	// l'esito (409 → già assegnato, 404 → inesistente).
	ft := &fakeTickets{
		claim: func(a ticket.Actor, id int64) (ticket.Ticket, error) {
			switch {
			case !a.IsAgent:
				return ticket.Ticket{}, ticket.ErrForbidden
			case id == 409:
				return ticket.Ticket{}, fmt.Errorf("claim ticket %d: %w", id, ticket.ErrAlreadyClaimed)
			case id == 404:
				return ticket.Ticket{}, fmt.Errorf("claim ticket %d: %w", id, ticket.ErrNotFound)
			}
			tk := sampleTicket(id)
			// Go: &a.UserID è l'indirizzo del campo: AssigneeID vuole un *string.
			tk.AssigneeID = &a.UserID
			return tk, nil
		},
		changeStatus: func(a ticket.Actor, id int64, status string) (ticket.Ticket, error) {
			switch {
			case id == 403:
				return ticket.Ticket{}, fmt.Errorf("change status: %w", ticket.ErrNotAssignee)
			case id == 409:
				return ticket.Ticket{}, fmt.Errorf("change status: %w: open -> resolved", ticket.ErrInvalidTransition)
			}
			tk := sampleTicket(id)
			tk.Status = ticket.Status(status)
			return tk, nil
		},
	}
	h := newHandler(withAuth(&fakeAuth{}), withTickets(ft))

	runCases(t, h, []errCase{
		{"claim ok", http.MethodPost, "/v1/tickets/1/claim", "", agentToken, http.StatusOK, ""},
		{"claim by requester", http.MethodPost, "/v1/tickets/1/claim", "", requesterToken, http.StatusForbidden, "forbidden"},
		{"claim conflict", http.MethodPost, "/v1/tickets/409/claim", "", agentToken, http.StatusConflict, "already_claimed"},
		{"claim not found", http.MethodPost, "/v1/tickets/404/claim", "", agentToken, http.StatusNotFound, "not_found"},
		{"claim without token", http.MethodPost, "/v1/tickets/1/claim", "", "", http.StatusUnauthorized, "unauthenticated"},
		{"status ok", http.MethodPatch, "/v1/tickets/1/status", `{"status":"in_progress"}`, agentToken, http.StatusOK, ""},
		{"status not assignee", http.MethodPatch, "/v1/tickets/403/status", `{"status":"in_progress"}`, agentToken, http.StatusForbidden, "not_assignee"},
		{"status bad transition", http.MethodPatch, "/v1/tickets/409/status", `{"status":"resolved"}`, agentToken, http.StatusConflict, "invalid_transition"},
	})
}
