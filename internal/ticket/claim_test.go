package ticket_test

import (
	"errors"
	"testing"

	"queuedesk/internal/ticket"
)

func TestClaim(t *testing.T) {
	tests := []struct {
		name      string
		actor     ticket.Actor
		preClaim  *ticket.Actor // agent che ha già fatto claim
		missing   bool
		wantErr   error
		wantStore bool // lo store deve essere interpellato?
	}{
		{"agent claims", agent, nil, false, nil, true},
		{"requester forbidden", requester, nil, false, ticket.ErrForbidden, false},
		{"already claimed", agent2, &agent, false, ticket.ErrAlreadyClaimed, true},
		{"missing ticket", agent, nil, true, ticket.ErrNotFound, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, st := newService()
			tk := st.add(ticket.Ticket{Title: "x", ReporterID: requester.UserID})
			if tt.preClaim != nil {
				// Go: copia in una variabile locale e poi &id: AssigneeID non deve
				// puntare dentro una struct condivisa fra i casi della tabella.
				id := tt.preClaim.UserID
				tk.AssigneeID = &id
				st.tickets[tk.ID] = tk
			}
			id := tk.ID
			if tt.missing {
				id = 999
			}

			got, err := svc.Claim(t.Context(), tt.actor, id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Claim error = %v, want %v", err, tt.wantErr)
			}
			if st.called("ClaimTicket") != tt.wantStore {
				t.Fatalf("store called = %v, want %v", !tt.wantStore, tt.wantStore)
			}
			if err == nil && (got.AssigneeID == nil || *got.AssigneeID != tt.actor.UserID) {
				t.Fatalf("assignee = %v, want %s", got.AssigneeID, tt.actor.UserID)
			}
		})
	}
}

func TestChangeStatus(t *testing.T) {
	tests := []struct {
		name    string
		actor   ticket.Actor
		from    ticket.Status
		status  string
		wantErr error
	}{
		{"assignee starts work", agent, ticket.StatusOpen, "in_progress", nil},
		{"assignee resolves", agent, ticket.StatusInProgress, "resolved", nil},
		{"requester forbidden", requester, ticket.StatusOpen, "in_progress", ticket.ErrForbidden},
		{"other agent is not assignee", agent2, ticket.StatusOpen, "in_progress", ticket.ErrNotAssignee},
		{"invalid status value", agent, ticket.StatusOpen, "done", ticket.ErrInvalidInput},
		{"transition not allowed", agent, ticket.StatusOpen, "resolved", ticket.ErrInvalidTransition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, st := newService()
			assignee := agent.UserID
			tk := st.add(ticket.Ticket{Title: "x", ReporterID: requester.UserID, Status: tt.from, AssigneeID: &assignee})

			got, err := svc.ChangeStatus(t.Context(), tt.actor, tk.ID, tt.status)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ChangeStatus error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && string(got.Status) != tt.status {
				t.Fatalf("status = %q, want %q", got.Status, tt.status)
			}
		})
	}
}

func TestCanTransition(t *testing.T) {
	all := []ticket.Status{ticket.StatusOpen, ticket.StatusInProgress, ticket.StatusResolved}
	// Go: un array [2]ticket.Status è confrontabile, quindi può fare da
	// chiave di una map (una slice no). Le coppie non elencate valgono false.
	allowed := map[[2]ticket.Status]bool{
		{ticket.StatusOpen, ticket.StatusInProgress}:     true,
		{ticket.StatusInProgress, ticket.StatusResolved}: true,
		{ticket.StatusInProgress, ticket.StatusOpen}:     true,
		{ticket.StatusResolved, ticket.StatusInProgress}: true,
	}
	for _, from := range all {
		for _, to := range all {
			want := allowed[[2]ticket.Status{from, to}]
			if got := ticket.CanTransition(from, to); got != want {
				// Go: Errorf e non Fatalf: il test prosegue e mostra tutte le coppie
				// sbagliate in una volta sola.
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
	if ticket.CanTransition("bogus", ticket.StatusOpen) {
		t.Error("unknown source status must not transition")
	}
}
