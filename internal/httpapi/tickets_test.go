package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"queuedesk/internal/ticket"
)

func TestCreateTicket(t *testing.T) {
	// Go: la closure assegna gotActor, una variabile del test: dopo la
	// richiesta si controlla cosa il fake ha ricevuto (un "spy").
	var gotActor ticket.Actor
	ft := &fakeTickets{create: func(a ticket.Actor, title, _ string) (ticket.Ticket, error) {
		gotActor = a
		if title == "" {
			return ticket.Ticket{}, fmt.Errorf("%w: title must be between 1 and 200 characters", ticket.ErrInvalidInput)
		}
		return sampleTicket(7), nil
	}}
	h := newHandler(withAuth(&fakeAuth{}), withTickets(ft))

	rec := serve(h, http.MethodPost, "/v1/tickets", `{"title":"VPN down","description":"since 9am"}`, bearer(requesterToken))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/tickets/7" {
		t.Fatalf("Location = %q", loc)
	}
	// Go: le parentesi attorno al letterale servono: senza, il parser
	// scambierebbe la { per l'inizio del blocco dell'if.
	if gotActor != (ticket.Actor{UserID: "req-1", IsAgent: false}) {
		t.Fatalf("actor = %+v, want the requester principal", gotActor)
	}
	body := decodeBody[map[string]any](t, rec)
	if body["assignee_id"] != nil || body["status"] != "open" {
		t.Fatalf("unexpected body %v", body)
	}

	rec = serve(h, http.MethodPost, "/v1/tickets", `{"title":""}`, bearer(requesterToken))
	assertError(t, rec, http.StatusBadRequest, "invalid_input")
}

func TestListTickets(t *testing.T) {
	var got ticket.ListQuery
	ft := &fakeTickets{list: func(a ticket.Actor, q ticket.ListQuery) ([]ticket.Ticket, error) {
		got = q
		if q.Limit == "abc" {
			return nil, fmt.Errorf("%w: limit must be an integer between 1 and 100", ticket.ErrInvalidInput)
		}
		if !a.IsAgent {
			return nil, nil // nessun ticket: la risposta deve essere [] e non null
		}
		return []ticket.Ticket{sampleTicket(1), sampleTicket(2)}, nil
	}}
	h := newHandler(withAuth(&fakeAuth{}), withTickets(ft))

	rec := serve(h, http.MethodGet, "/v1/tickets?status=open&limit=5", "", bearer(agentToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body)
	}
	if got != (ticket.ListQuery{Status: "open", Limit: "5"}) {
		t.Fatalf("query = %+v", got)
	}
	// Go: []json.RawMessage lascia ogni ticket come JSON grezzo: qui conta
	// solo quanti sono, non il loro contenuto.
	if body := decodeBody[map[string][]json.RawMessage](t, rec); len(body["tickets"]) != 2 {
		t.Fatalf("got %d tickets, want 2", len(body["tickets"]))
	}

	rec = serve(h, http.MethodGet, "/v1/tickets", "", bearer(requesterToken))
	if rec.Body.String() != "{\"tickets\":[]}\n" {
		t.Fatalf("empty list body = %q, want {\"tickets\":[]}", rec.Body)
	}

	assertError(t, serve(h, http.MethodGet, "/v1/tickets?limit=abc", "", bearer(agentToken)), http.StatusBadRequest, "invalid_input")
}

func TestTicketErrorMapping(t *testing.T) {
	ft := &fakeTickets{
		get: func(a ticket.Actor, id int64) (ticket.Ticket, error) {
			if id == 404 {
				return ticket.Ticket{}, fmt.Errorf("get ticket %d: %w", id, ticket.ErrNotFound)
			}
			return sampleTicket(id), nil
		},
		events: func(a ticket.Actor, id int64) ([]ticket.Event, error) {
			if id == 404 {
				return nil, ticket.ErrNotFound
			}
			return []ticket.Event{{ID: 1, TicketID: id, Kind: ticket.EventCreated, Payload: json.RawMessage(`{"title":"VPN down"}`)}}, nil
		},
	}
	h := newHandler(withAuth(&fakeAuth{}), withTickets(ft))

	runCases(t, h, []errCase{
		{"get ok", http.MethodGet, "/v1/tickets/1", "", requesterToken, http.StatusOK, ""},
		{"get not found", http.MethodGet, "/v1/tickets/404", "", requesterToken, http.StatusNotFound, "not_found"},
		{"get invalid id", http.MethodGet, "/v1/tickets/abc", "", requesterToken, http.StatusBadRequest, "invalid_id"},
		{"get zero id", http.MethodGet, "/v1/tickets/0", "", requesterToken, http.StatusBadRequest, "invalid_id"},
		{"get without token", http.MethodGet, "/v1/tickets/1", "", "", http.StatusUnauthorized, "unauthenticated"},
		{"events ok", http.MethodGet, "/v1/tickets/1/events", "", requesterToken, http.StatusOK, ""},
		{"events not found", http.MethodGet, "/v1/tickets/404/events", "", requesterToken, http.StatusNotFound, "not_found"},
	})
}

func TestUnexpectedErrorsDoNotLeak(t *testing.T) {
	ft := &fakeTickets{get: func(ticket.Actor, int64) (ticket.Ticket, error) {
		return ticket.Ticket{}, errors.New(`pq: relation "tickets" does not exist`)
	}}
	h := newHandler(withAuth(&fakeAuth{}), withTickets(ft))
	rec := serve(h, http.MethodGet, "/v1/tickets/1", "", bearer(agentToken))
	assertError(t, rec, http.StatusInternalServerError, "internal_error")
	if strings.Contains(rec.Body.String(), "relation") {
		t.Fatal("internal error details leaked to the client")
	}
}

// TestClientCancelIsLoggedAs499: se il client chiude la connessione, il log di
// accesso non deve registrare un 200 (nessuno ha ricevuto una risposta).
func TestClientCancelIsLoggedAs499(t *testing.T) {
	ft := &fakeTickets{get: func(ticket.Actor, int64) (ticket.Ticket, error) {
		return ticket.Ticket{}, fmt.Errorf("get ticket 1: %w", context.Canceled)
	}}
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := newHandler(withAuth(&fakeAuth{}), withTickets(ft), withLogger(log))

	// Go: *r = *r.WithContext(ctx) sostituisce la richiesta a cui punta r con
	// una copia che usa il context già cancellato, come se il client fosse
	// sparito durante la richiesta.
	cancelled := func(r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		*r = *r.WithContext(ctx)
	}
	rec := serve(h, http.MethodGet, "/v1/tickets/1", "", bearer(agentToken), cancelled)
	if rec.Code != 499 {
		t.Fatalf("status = %d, want 499", rec.Code)
	}
	if !strings.Contains(buf.String(), `"status":499`) {
		t.Fatalf("access log does not record 499:\n%s", buf.String())
	}
}
