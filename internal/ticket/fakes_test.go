package ticket_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"queuedesk/internal/ticket"
)

// Go: i file *_test.go non finiscono nel binario: fake e helper si possono
// definire qui senza appesantire il codice di produzione.

// fakeStore è uno Store in memoria con le stesse regole di quello reale.
type fakeStore struct {
	mu      sync.Mutex
	tickets map[int64]ticket.Ticket
	events  []ticket.Event
	nextID  int64
	calls   []string // metodi invocati, per verificare che il service non chiami lo store a vuoto
	lastF   ticket.ListFilter
	err     error // se valorizzato, ogni metodo lo restituisce
}

func newFakeStore() *fakeStore {
	return &fakeStore{tickets: map[int64]ticket.Ticket{}}
}

// record va chiamato con f.mu già preso (lo fanno tutti i metodi).
func (f *fakeStore) record(name string) error {
	f.calls = append(f.calls, name)
	return f.err
}

func (f *fakeStore) add(t ticket.Ticket) ticket.Ticket {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	t.ID = f.nextID
	if t.Status == "" {
		t.Status = ticket.StatusOpen
	}
	f.tickets[t.ID] = t
	return t
}

func (f *fakeStore) CreateTicket(_ context.Context, nt ticket.NewTicket) (ticket.Ticket, error) {
	// Go: qui niente defer: add prende di nuovo f.mu, e un sync.Mutex non è
	// rientrante. Tenerlo fino alla fine bloccherebbe la goroutine per sempre.
	f.mu.Lock()
	err := f.record("CreateTicket")
	f.mu.Unlock()
	if err != nil {
		return ticket.Ticket{}, err
	}
	now := time.Now()
	return f.add(ticket.Ticket{Title: nt.Title, Description: nt.Description, ReporterID: nt.ReporterID, CreatedAt: now, UpdatedAt: now}), nil
}

func (f *fakeStore) ListTickets(_ context.Context, lf ticket.ListFilter) ([]ticket.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("ListTickets"); err != nil {
		return nil, err
	}
	f.lastF = lf
	var out []ticket.Ticket
	for _, t := range f.tickets {
		if (lf.ReporterID == "" || t.ReporterID == lf.ReporterID) && (lf.Status == "" || t.Status == lf.Status) {
			out = append(out, t)
		}
	}
	// Go: le map non hanno ordine: ordiniamo come lo store reale (dal più
	// recente). SortFunc vuole un risultato < 0, 0 o > 0; b - a dà l'ordine
	// decrescente.
	slices.SortFunc(out, func(a, b ticket.Ticket) int { return int(b.ID - a.ID) })
	if len(out) > lf.Limit {
		// Go: out[:n] è una sotto-slice dei primi n elementi, senza copia.
		out = out[:lf.Limit]
	}
	return out, nil
}

func (f *fakeStore) TicketByID(_ context.Context, id int64) (ticket.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("TicketByID"); err != nil {
		return ticket.Ticket{}, err
	}
	t, ok := f.tickets[id]
	if !ok {
		return ticket.Ticket{}, ticket.ErrNotFound
	}
	return t, nil
}

func (f *fakeStore) ListEvents(_ context.Context, id int64) ([]ticket.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("ListEvents"); err != nil {
		return nil, err
	}
	var out []ticket.Event
	for _, e := range f.events {
		if e.TicketID == id {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeStore) called(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Go: il package slices (Go 1.21+) ha le operazioni comuni su slice:
	// Contains, Index, Sort, Equal...
	return slices.Contains(f.calls, name)
}

var (
	// Go: variabili di package condivise da tutti i test. Si leggono soltanto:
	// modificarle in un test influenzerebbe gli altri.
	requester = ticket.Actor{UserID: "req-1"}
	other     = ticket.Actor{UserID: "req-2"}
	agent     = ticket.Actor{UserID: "agent-1", IsAgent: true}
	agent2    = ticket.Actor{UserID: "agent-2", IsAgent: true}
)

// newService crea il service con uno store finto e senza notifiche.
func newService() (*ticket.Service, *fakeStore) {
	st := newFakeStore()
	// Go: due risultati: il service da testare e lo store, per preparare i
	// dati e controllare le chiamate.
	return ticket.NewService(st, nil), st
}
