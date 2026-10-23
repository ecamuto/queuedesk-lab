package postgres_test

import (
	"encoding/json"
	"errors"
	"testing"

	"queuedesk/internal/postgres"
	"queuedesk/internal/ticket"
)

func TestCreateTicketWritesAuditEvent(t *testing.T) {
	pool := testDB(t)
	u := mustUser(t, pool, "ada@example.com", "requester")
	repo := postgres.NewTicketRepo(pool)
	ctx := t.Context()

	tk, err := repo.CreateTicket(ctx, ticket.NewTicket{Title: "VPN down", Description: "since 9am", ReporterID: u.ID})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if tk.ID == 0 || tk.Status != ticket.StatusOpen || tk.AssigneeID != nil || tk.ReporterID != u.ID {
		t.Fatalf("unexpected ticket %+v", tk)
	}

	evs, err := repo.ListEvents(ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evs) != 1 || evs[0].Kind != ticket.EventCreated || evs[0].ActorID != u.ID {
		t.Fatalf("unexpected events %+v", evs)
	}
	// Go: json.Unmarshal riempie la map attraverso il puntatore &payload; una
	// map nil va bene, Unmarshal la crea da solo.
	var payload map[string]string
	if err := json.Unmarshal(evs[0].Payload, &payload); err != nil || payload["title"] != "VPN down" {
		t.Fatalf("payload = %s (%v)", evs[0].Payload, err)
	}
}

func TestCreateTicketIsAtomic(t *testing.T) {
	pool := testDB(t)
	repo := postgres.NewTicketRepo(pool)

	// Reporter inesistente: la FK fallisce e non deve restare nulla.
	_, err := repo.CreateTicket(t.Context(), ticket.NewTicket{Title: "x", ReporterID: "00000000-0000-0000-0000-000000000000"})
	if err == nil {
		t.Fatal("expected foreign key error")
	}
	var tickets, events int
	// Go: Scan con due destinazioni: una per colonna, nell'ordine.
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM tickets), (SELECT count(*) FROM ticket_events)`).Scan(&tickets, &events); err != nil {
		t.Fatal(err)
	}
	if tickets != 0 || events != 0 {
		t.Fatalf("found %d tickets and %d events after a failed create", tickets, events)
	}
}

func TestListTicketsFilters(t *testing.T) {
	pool := testDB(t)
	ada := mustUser(t, pool, "ada@example.com", "requester")
	bob := mustUser(t, pool, "bob@example.com", "requester")
	repo := postgres.NewTicketRepo(pool)
	ctx := t.Context()

	var adaIDs []int64
	for _, title := range []string{"one", "two", "three"} {
		tk, err := repo.CreateTicket(ctx, ticket.NewTicket{Title: title, ReporterID: ada.ID})
		if err != nil {
			t.Fatal(err)
		}
		adaIDs = append(adaIDs, tk.ID)
	}
	if _, err := repo.CreateTicket(ctx, ticket.NewTicket{Title: "bob's", ReporterID: bob.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tickets SET status = 'resolved' WHERE id = $1`, adaIDs[0]); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		f       ticket.ListFilter
		wantIDs []int64
	}{
		{"by reporter, newest first", ticket.ListFilter{ReporterID: ada.ID, Limit: 10}, []int64{adaIDs[2], adaIDs[1], adaIDs[0]}},
		{"by status", ticket.ListFilter{Status: ticket.StatusResolved, Limit: 10}, []int64{adaIDs[0]}},
		{"reporter and status", ticket.ListFilter{ReporterID: ada.ID, Status: ticket.StatusOpen, Limit: 10}, []int64{adaIDs[2], adaIDs[1]}},
		// Go: nil e []int64{} sono diversi: nil vuol dire "controlla solo quanti",
		// una slice vuota "nessun risultato atteso".
		{"limit", ticket.ListFilter{Limit: 2}, nil}, // controllato sotto solo il numero
		{"no match", ticket.ListFilter{ReporterID: bob.ID, Status: ticket.StatusResolved, Limit: 10}, []int64{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.ListTickets(ctx, tt.f)
			if err != nil {
				t.Fatalf("ListTickets: %v", err)
			}
			if tt.wantIDs == nil {
				if len(got) != tt.f.Limit {
					t.Fatalf("got %d tickets, want %d", len(got), tt.f.Limit)
				}
				return
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("got %d tickets, want %d", len(got), len(tt.wantIDs))
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Fatalf("position %d: id %d, want %d", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestTicketByIDNotFound(t *testing.T) {
	pool := testDB(t)
	if _, err := postgres.NewTicketRepo(pool).TicketByID(t.Context(), 424242); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("TicketByID = %v, want ErrNotFound", err)
	}
}
