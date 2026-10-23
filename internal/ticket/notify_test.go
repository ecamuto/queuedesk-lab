package ticket_test

import (
	"slices"
	"testing"

	"queuedesk/internal/notify"
	"queuedesk/internal/ticket"
)

// TestPublishesAfterSuccess: ogni mutazione riuscita accoda una notifica
// destinata al reporter del ticket.
func TestPublishesAfterSuccess(t *testing.T) {
	svc, _, pub := newServiceWithPublisher()
	ctx := t.Context()

	tk, err := svc.Create(ctx, requester, "VPN down", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Claim(ctx, agent, tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangeStatus(ctx, agent, tk.ID, "in_progress"); err != nil {
		t.Fatal(err)
	}

	// Go: due slice si confrontano con slices.Equal (stessa lunghezza, stessi
	// elementi nello stesso ordine), non con ==.
	if want := []string{"created", "claimed", "status_changed"}; !slices.Equal(pub.kinds(), want) {
		t.Fatalf("notifications = %v, want %v", pub.kinds(), want)
	}
	for _, n := range pub.sent {
		if n.TicketID != tk.ID || n.RecipientID != requester.UserID {
			t.Fatalf("unexpected notification %+v", n)
		}
	}
	if last := pub.sent[2]; last.ActorID != agent.UserID || last.Status != "in_progress" {
		t.Fatalf("unexpected status notification %+v", last)
	}
}

// TestNoPublishOnFailure: un'operazione fallita non deve notificare nulla.
func TestNoPublishOnFailure(t *testing.T) {
	svc, st, pub := newServiceWithPublisher()
	ctx := t.Context()
	tk := st.add(ticket.Ticket{Title: "x", ReporterID: requester.UserID})

	// Go: "_, _ =" scarta entrambi i risultati in modo esplicito: qui conta
	// solo che nessuna notifica venga pubblicata.
	_, _ = svc.Create(ctx, requester, "", "")                 // input non valido
	_, _ = svc.Claim(ctx, requester, tk.ID)                   // ruolo sbagliato
	_, _ = svc.ChangeStatus(ctx, agent, tk.ID, "in_progress") // non assegnato
	_, _ = svc.Claim(ctx, agent, 999)                         // inesistente

	if got := pub.kinds(); len(got) != 0 {
		t.Fatalf("notifications published for failed operations: %v", got)
	}
}

// TestOperationsSucceedWhenQueueIsFull: la notifica è best effort.
func TestOperationsSucceedWhenQueueIsFull(t *testing.T) {
	for _, pubErr := range []error{notify.ErrQueueFull, notify.ErrClosed} {
		// Go: il nome del sotto-test può essere calcolato: qui è il messaggio
		// dell'errore simulato ("notify: queue full").
		t.Run(pubErr.Error(), func(t *testing.T) {
			svc, _, pub := newServiceWithPublisher()
			pub.err = pubErr
			ctx := t.Context()

			tk, err := svc.Create(ctx, requester, "VPN down", "")
			if err != nil {
				t.Fatalf("Create must not fail when the notification is dropped: %v", err)
			}
			if _, err := svc.Claim(ctx, agent, tk.ID); err != nil {
				t.Fatalf("Claim must not fail when the notification is dropped: %v", err)
			}
		})
	}
}
