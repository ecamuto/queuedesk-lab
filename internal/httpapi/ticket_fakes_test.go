package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"queuedesk/internal/httpapi"
	"queuedesk/internal/ticket"
)

// Un fake che riceve una chiamata non prevista dal test restituisce questo
// errore (→ 500): il test fallisce invece di passare per caso.
var errUnexpectedCall = errors.New("unexpected call to fake")

// fakeTickets delega a funzioni impostate dal singolo test.
//
// Go: campi di tipo funzione al posto di metodi fissi: una sola struct
// serve a tutti i test, ognuno imposta solo i campi che gli servono.
// Un campo funzione non impostato vale nil (vedi i controlli sotto).
type fakeTickets struct {
	create       func(ticket.Actor, string, string) (ticket.Ticket, error)
	list         func(ticket.Actor, ticket.ListQuery) ([]ticket.Ticket, error)
	get          func(ticket.Actor, int64) (ticket.Ticket, error)
	claim        func(ticket.Actor, int64) (ticket.Ticket, error)
	changeStatus func(ticket.Actor, int64, string) (ticket.Ticket, error)
	events       func(ticket.Actor, int64) ([]ticket.Event, error)
}

func (f *fakeTickets) Create(_ context.Context, a ticket.Actor, title, desc string) (ticket.Ticket, error) {
	if f.create == nil {
		return ticket.Ticket{}, errUnexpectedCall
	}
	return f.create(a, title, desc)
}

func (f *fakeTickets) List(_ context.Context, a ticket.Actor, q ticket.ListQuery) ([]ticket.Ticket, error) {
	if f.list == nil {
		return nil, errUnexpectedCall
	}
	return f.list(a, q)
}

func (f *fakeTickets) Get(_ context.Context, a ticket.Actor, id int64) (ticket.Ticket, error) {
	if f.get == nil {
		return ticket.Ticket{}, errUnexpectedCall
	}
	return f.get(a, id)
}

func (f *fakeTickets) Claim(_ context.Context, a ticket.Actor, id int64) (ticket.Ticket, error) {
	if f.claim == nil {
		return ticket.Ticket{}, errUnexpectedCall
	}
	return f.claim(a, id)
}

func (f *fakeTickets) ChangeStatus(_ context.Context, a ticket.Actor, id int64, status string) (ticket.Ticket, error) {
	if f.changeStatus == nil {
		return ticket.Ticket{}, errUnexpectedCall
	}
	return f.changeStatus(a, id, status)
}

func (f *fakeTickets) Events(_ context.Context, a ticket.Actor, id int64) ([]ticket.Event, error) {
	if f.events == nil {
		return nil, errUnexpectedCall
	}
	return f.events(a, id)
}

func withTickets(ft *fakeTickets) option { return func(d *httpapi.Deps) { d.Tickets = ft } }

func sampleTicket(id int64) ticket.Ticket {
	return ticket.Ticket{ID: id, Title: "VPN down", Status: ticket.StatusOpen, ReporterID: "req-1"}
}

// errCase è un caso "richiesta → status e codice attesi".
//
// Go: name, method, path, body, token string dichiara cinque campi dello
// stesso tipo su una riga.
type errCase struct {
	name, method, path, body, token string
	want                            int
	code                            string // "" = successo
}

func runCases(t *testing.T, h http.Handler, cases []errCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var opts []reqOpt
			if tt.token != "" {
				opts = append(opts, bearer(tt.token))
			}
			rec := serve(h, tt.method, tt.path, tt.body, opts...)
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
