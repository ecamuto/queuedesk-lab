package ticket_test

import (
	"context"

	"queuedesk/internal/ticket"
)

// Go: i metodi di un tipo possono stare in più file dello stesso package:
// fakeStore è definito in fakes_test.go, questi due metodi si aggiungono dalla
// tappa 4. Il mutex rende il claim atomico anche nel fake, come l'UPDATE
// condizionale in PostgreSQL.
func (f *fakeStore) ClaimTicket(_ context.Context, id int64, agentID string) (ticket.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("ClaimTicket"); err != nil {
		return ticket.Ticket{}, err
	}
	t, ok := f.tickets[id]
	switch {
	case !ok:
		return ticket.Ticket{}, ticket.ErrNotFound
	case t.AssigneeID != nil:
		return ticket.Ticket{}, ticket.ErrAlreadyClaimed
	}
	// Go: t è una copia del valore nella map: modificarla non cambia la map.
	// Per questo dopo la modifica la si riscrive con f.tickets[id] = t.
	t.AssigneeID = &agentID
	f.tickets[id] = t
	return t, nil
}

func (f *fakeStore) UpdateStatus(_ context.Context, id int64, actorID string, to ticket.Status) (ticket.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("UpdateStatus"); err != nil {
		return ticket.Ticket{}, err
	}
	t, ok := f.tickets[id]
	switch {
	case !ok:
		return ticket.Ticket{}, ticket.ErrNotFound
	case t.AssigneeID == nil || *t.AssigneeID != actorID:
		return ticket.Ticket{}, ticket.ErrNotAssignee
	case !ticket.CanTransition(t.Status, to):
		return ticket.Ticket{}, ticket.ErrInvalidTransition
	}
	t.Status = to
	f.tickets[id] = t
	return t, nil
}
