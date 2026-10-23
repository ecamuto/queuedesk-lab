package ticket_test

import (
	"errors"
	"strings"
	"testing"

	"queuedesk/internal/ticket"
)

func TestCreateValidation(t *testing.T) {
	tests := []struct {
		name        string
		title, desc string
		wantErr     error
		wantTitle   string
	}{
		{"valid, trimmed", "  Printer on fire  ", "3rd floor", nil, "Printer on fire"},
		{"empty description allowed", "VPN down", "", nil, "VPN down"},
		// "é" occupa 2 byte: con len il titolo sembrerebbe lungo il doppio. Il
		// caso verifica che si contino i caratteri (rune).
		{"title exactly max length", strings.Repeat("é", ticket.MaxTitleLen), "", nil, strings.Repeat("é", ticket.MaxTitleLen)},
		{"empty title", "", "x", ticket.ErrInvalidInput, ""},
		{"blank title", "   \t ", "x", ticket.ErrInvalidInput, ""},
		{"title too long", strings.Repeat("a", ticket.MaxTitleLen+1), "", ticket.ErrInvalidInput, ""},
		{"description too long", "ok", strings.Repeat("a", ticket.MaxDescriptionLen+1), ticket.ErrInvalidInput, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, st := newService()
			got, err := svc.Create(t.Context(), requester, tt.title, tt.desc)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Create error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				if st.called("CreateTicket") {
					t.Fatal("store called with invalid input")
				}
				return
			}
			if got.Title != tt.wantTitle || got.ReporterID != requester.UserID || got.Status != ticket.StatusOpen {
				t.Fatalf("unexpected ticket %+v", got)
			}
		})
	}
}

func TestStoreErrorsAreWrapped(t *testing.T) {
	svc, st := newService()
	boom := errors.New("connection reset")
	st.err = boom

	_, err := svc.Create(t.Context(), requester, "VPN down", "")
	// Go: errors.Is attraversa i %w: verifica che il service abbia avvolto
	// l'errore dello store, non che lo abbia sostituito.
	if !errors.Is(err, boom) {
		t.Fatalf("error %v does not wrap the store error", err)
	}
	if !strings.Contains(err.Error(), "create ticket") {
		t.Fatalf("error %q has no context", err)
	}
}

func TestListFilter(t *testing.T) {
	tests := []struct {
		name    string
		actor   ticket.Actor
		q       ticket.ListQuery
		want    ticket.ListFilter
		wantErr error
	}{
		{"requester sees only own", requester, ticket.ListQuery{}, ticket.ListFilter{ReporterID: "req-1", Limit: ticket.DefaultListLimit}, nil},
		{"agent sees all", agent, ticket.ListQuery{}, ticket.ListFilter{Limit: ticket.DefaultListLimit}, nil},
		{"status and limit", agent, ticket.ListQuery{Status: "resolved", Limit: "5"}, ticket.ListFilter{Status: ticket.StatusResolved, Limit: 5}, nil},
		{"max limit", requester, ticket.ListQuery{Limit: "100"}, ticket.ListFilter{ReporterID: "req-1", Limit: 100}, nil},
		{"unknown status", agent, ticket.ListQuery{Status: "closed"}, ticket.ListFilter{}, ticket.ErrInvalidInput},
		{"limit zero", agent, ticket.ListQuery{Limit: "0"}, ticket.ListFilter{}, ticket.ErrInvalidInput},
		{"limit too big", agent, ticket.ListQuery{Limit: "101"}, ticket.ListFilter{}, ticket.ErrInvalidInput},
		{"limit not a number", agent, ticket.ListQuery{Limit: "ten"}, ticket.ListFilter{}, ticket.ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, st := newService()
			_, err := svc.List(t.Context(), tt.actor, tt.q)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("List error = %v, want %v", err, tt.wantErr)
			}
			// Go: ListFilter contiene solo string e int: si confronta con != in un
			// colpo solo.
			if err == nil && st.lastF != tt.want {
				t.Fatalf("filter = %+v, want %+v", st.lastF, tt.want)
			}
		})
	}
}

func TestGetVisibility(t *testing.T) {
	tests := []struct {
		name    string
		actor   ticket.Actor
		wantErr error
	}{
		{"owner", requester, nil},
		{"agent", agent, nil},
		{"other requester gets 404, not 403", other, ticket.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, st := newService()
			tk := st.add(ticket.Ticket{Title: "x", ReporterID: requester.UserID})

			if _, err := svc.Get(t.Context(), tt.actor, tk.ID); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Get error = %v, want %v", err, tt.wantErr)
			}
			if _, err := svc.Events(t.Context(), tt.actor, tk.ID); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Events error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	t.Run("missing ticket", func(t *testing.T) {
		svc, _ := newService()
		if _, err := svc.Get(t.Context(), agent, 42); !errors.Is(err, ticket.ErrNotFound) {
			t.Fatalf("Get error = %v, want ErrNotFound", err)
		}
	})
}
