package postgres_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"queuedesk/internal/postgres"
	"queuedesk/internal/ticket"
)

// TestClaimConcurrentSingleWinner fa partire N claim sullo stesso ticket nello
// stesso istante. Nessuno sleep: ogni goroutine segnala di essere pronta
// (ready) e poi resta bloccata su start; chiudere start le libera tutte
// insieme. Deve esserci esattamente un vincitore e un solo evento "claimed".
//
// Go: con -race il race detector controlla anche che le goroutine del test
// non condividano memoria senza sincronizzazione: i risultati passano solo
// dal channel results.
func TestClaimConcurrentSingleWinner(t *testing.T) {
	for _, contenders := range []int{2, 8} {
		t.Run(fmt.Sprintf("%d agents", contenders), func(t *testing.T) {
			pool := testDB(t)
			repo := postgres.NewTicketRepo(pool)
			reporter := mustUser(t, pool, "ada@example.com", "requester")
			agents := make([]string, contenders)
			for i := range agents {
				agents[i] = mustUser(t, pool, fmt.Sprintf("agent%d@example.com", i), "agent").ID
			}

			// Più round aumentano la probabilità di vera sovrapposizione.
			for round := range 10 {
				tk, err := repo.CreateTicket(t.Context(), ticket.NewTicket{Title: fmt.Sprintf("round %d", round), ReporterID: reporter.ID})
				if err != nil {
					t.Fatal(err)
				}
				assertSingleWinner(t, pool, repo, tk.ID, agents)
			}
		})
	}
}

func assertSingleWinner(t *testing.T, pool *pgxpool.Pool, repo *postgres.TicketRepo, ticketID int64, agents []string) {
	t.Helper()
	ctx := t.Context()

	// Go: un tipo dichiarato dentro una funzione è visibile solo lì: basta
	// per raccogliere i risultati di questo helper.
	type result struct {
		agentID string
		err     error
	}
	results := make(chan result, len(agents))
	start := make(chan struct{})
	// Go: due WaitGroup con ruoli diversi: ready aspetta che tutte le
	// goroutine siano partite, done che abbiano finito il claim.
	var ready, done sync.WaitGroup

	for _, agentID := range agents {
		// Go: Add va chiamato prima di avviare la goroutine, non dentro: altrimenti
		// ready.Wait potrebbe vedere il contatore a zero e proseguire troppo presto.
		// done.Go fa Add e Done da solo.
		ready.Add(1)
		done.Go(func() {
			ready.Done()
			<-start // barriera: tutte partono insieme
			_, err := repo.ClaimTicket(ctx, ticketID, agentID)
			results <- result{agentID, err}
		})
	}
	ready.Wait()
	close(start)
	done.Wait()
	// Go: chiuso solo dopo done.Wait: nessuno vi scrive più, e il range qui
	// sotto termina dopo l'ultimo risultato.
	close(results)

	var winners []string
	for r := range results {
		switch {
		case r.err == nil:
			winners = append(winners, r.agentID)
		case errors.Is(r.err, ticket.ErrAlreadyClaimed):
			// Go: un case con solo un commento non fa nulla ed è lecito.
			// atteso per i perdenti
		default:
			t.Fatalf("agent %s: unexpected error %v", r.agentID, r.err)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("ticket %d: %d winners %v, want exactly 1", ticketID, len(winners), winners)
	}

	var claimed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ticket_events WHERE ticket_id = $1 AND kind = 'claimed'`, ticketID).Scan(&claimed); err != nil {
		t.Fatal(err)
	}
	if claimed != 1 {
		t.Fatalf("ticket %d: %d claimed events, want 1", ticketID, claimed)
	}

	tk, err := repo.TicketByID(ctx, ticketID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.AssigneeID == nil || *tk.AssigneeID != winners[0] {
		t.Fatalf("ticket %d: assignee %v, want winner %s", ticketID, tk.AssigneeID, winners[0])
	}
}

func TestClaimErrors(t *testing.T) {
	pool := testDB(t)
	repo := postgres.NewTicketRepo(pool)
	reporter := mustUser(t, pool, "ada@example.com", "requester")
	agent := mustUser(t, pool, "grace@example.com", "agent")
	ctx := t.Context()

	if _, err := repo.ClaimTicket(ctx, 424242, agent.ID); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("claim missing ticket = %v, want ErrNotFound", err)
	}

	tk, err := repo.CreateTicket(ctx, ticket.NewTicket{Title: "x", ReporterID: reporter.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClaimTicket(ctx, tk.ID, agent.ID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	// Anche lo stesso agent riceve 409: il claim non è idempotente.
	if _, err := repo.ClaimTicket(ctx, tk.ID, agent.ID); !errors.Is(err, ticket.ErrAlreadyClaimed) {
		t.Fatalf("second claim = %v, want ErrAlreadyClaimed", err)
	}
}

func TestUpdateStatus(t *testing.T) {
	pool := testDB(t)
	ada := mustUser(t, pool, "ada@example.com", "requester")
	grace := mustUser(t, pool, "grace@example.com", "agent")
	linus := mustUser(t, pool, "linus@example.com", "agent")
	repo := postgres.NewTicketRepo(pool)
	ctx := t.Context()

	tk, err := repo.CreateTicket(ctx, ticket.NewTicket{Title: "Printer", ReporterID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repo.UpdateStatus(ctx, tk.ID, grace.ID, ticket.StatusInProgress); !errors.Is(err, ticket.ErrNotAssignee) {
		t.Fatalf("UpdateStatus on unassigned ticket = %v, want ErrNotAssignee", err)
	}
	if _, err := repo.ClaimTicket(ctx, tk.ID, grace.ID); err != nil {
		t.Fatalf("ClaimTicket: %v", err)
	}

	// Go: qui il ciclo non usa t.Run: i passi sono in sequenza e ognuno
	// dipende dallo stato lasciato dal precedente (non sono casi indipendenti).
	steps := []struct {
		name    string
		actor   string
		to      ticket.Status
		wantErr error
	}{
		{"other agent", linus.ID, ticket.StatusInProgress, ticket.ErrNotAssignee},
		{"skip in_progress", grace.ID, ticket.StatusResolved, ticket.ErrInvalidTransition},
		{"start work", grace.ID, ticket.StatusInProgress, nil},
		{"resolve", grace.ID, ticket.StatusResolved, nil},
	}
	for _, s := range steps {
		got, err := repo.UpdateStatus(ctx, tk.ID, s.actor, s.to)
		if !errors.Is(err, s.wantErr) {
			t.Fatalf("%s: UpdateStatus = %v, want %v", s.name, err, s.wantErr)
		}
		if err == nil && (got.Status != s.to || !got.UpdatedAt.After(tk.UpdatedAt)) {
			t.Fatalf("%s: unexpected ticket %+v", s.name, got)
		}
	}

	if _, err := repo.UpdateStatus(ctx, 424242, grace.ID, ticket.StatusOpen); !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("UpdateStatus on missing ticket = %v, want ErrNotFound", err)
	}

	evs, err := repo.ListEvents(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []ticket.EventKind
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	want := []ticket.EventKind{ticket.EventCreated, ticket.EventClaimed, ticket.EventStatusChanged, ticket.EventStatusChanged}
	if len(kinds) != len(want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("event kinds = %v, want %v", kinds, want)
		}
	}
	var last map[string]string
	if err := json.Unmarshal(evs[3].Payload, &last); err != nil || last["from"] != "in_progress" || last["to"] != "resolved" {
		t.Fatalf("last payload = %s (%v)", evs[3].Payload, err)
	}
}
