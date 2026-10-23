//go:build acceptance

// Acceptance test della tappa corrente: girano con `make test-acceptance`.
// Falliscono finché i TODO della tappa non sono completati (vedi TAPPA.md).

package notify_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"queuedesk/internal/notify"
)

// newQueue crea la coda o ferma il test: i test restano corti.
// Go: passare nil come logger è lecito, New usa un logger che scarta tutto.
func newQueue(t *testing.T, size, workers int, h notify.Handler) *notify.Queue {
	t.Helper()
	q, err := notify.New(size, workers, h, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return q
}

func TestNewValidatesArguments(t *testing.T) {
	// Go: una funzione anonima assegnata a una variabile; ha la firma di
	// notify.Handler, quindi si mette nel campo h della tabella.
	ok := func(context.Context, notify.Notification) error { return nil }
	tests := []struct {
		name          string
		size, workers int
		h             notify.Handler
	}{
		{"zero size", 0, 1, ok},
		{"zero workers", 1, 0, ok},
		{"nil handler", 1, 1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := notify.New(tt.size, tt.workers, tt.h, nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestQueueDeliversEveryNotification(t *testing.T) {
	const total = 20
	// Go: il channel con buffer total raccoglie gli ID consegnati dai worker:
	// l'handler non si blocca mai sull'invio, anche se nessuno sta leggendo.
	got := make(chan int64, total)
	q := newQueue(t, total, 4, func(_ context.Context, n notify.Notification) error {
		got <- n.TicketID
		return nil
	})
	q.Start(t.Context())

	for i := range total {
		if err := q.TryPublish(notify.Notification{TicketID: int64(i)}); err != nil {
			t.Fatalf("TryPublish %d: %v", i, err)
		}
	}
	if err := q.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// Go: dopo Shutdown nessun worker scrive più su got: lo si può chiudere,
	// e il range qui sotto finisce quando ha letto tutti i valori.
	close(got)

	seen := map[int64]bool{}
	for id := range got {
		seen[id] = true
	}
	if len(seen) != total {
		t.Fatalf("delivered %d distinct notifications, want %d", len(seen), total)
	}
	if s := q.Stats(); s.Published != total || s.Delivered != total || s.Dropped != 0 {
		t.Fatalf("unexpected stats %+v", s)
	}
}

func TestTryPublishWhenFullDropsWithoutBlocking(t *testing.T) {
	// Nessun worker avviato: la coda (capacità 2) si riempie e resta piena.
	q := newQueue(t, 2, 1, func(context.Context, notify.Notification) error { return nil })

	for i := range 2 {
		if err := q.TryPublish(notify.Notification{TicketID: int64(i)}); err != nil {
			t.Fatalf("TryPublish %d: %v", i, err)
		}
	}
	if err := q.TryPublish(notify.Notification{TicketID: 99}); !errors.Is(err, notify.ErrQueueFull) {
		t.Fatalf("TryPublish on full queue = %v, want ErrQueueFull", err)
	}
	if s := q.Stats(); s.Published != 2 || s.Dropped != 1 {
		t.Fatalf("unexpected stats %+v", s)
	}
}

func TestTryPublishAfterShutdownReturnsErrClosed(t *testing.T) {
	q := newQueue(t, 4, 1, func(context.Context, notify.Notification) error { return nil })
	q.Start(t.Context())
	if err := q.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.TryPublish(notify.Notification{}); !errors.Is(err, notify.ErrClosed) {
		t.Fatalf("TryPublish after Shutdown = %v, want ErrClosed", err)
	}
	// Shutdown è idempotente.
	if err := q.Shutdown(t.Context()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

// Produttori concorrenti e Shutdown simultaneo: con -race verifica che non
// ci siano data race né panic "send on closed channel".
func TestConcurrentPublishDuringShutdown(t *testing.T) {
	q := newQueue(t, 8, 2, func(context.Context, notify.Notification) error { return nil })
	q.Start(t.Context())

	// Go: start è un "via" comune: le 16 goroutine aspettano <-start e
	// close(start) le sblocca tutte insieme, così la contesa è reale.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			for range 100 {
				err := q.TryPublish(notify.Notification{})
				if err != nil && !errors.Is(err, notify.ErrQueueFull) && !errors.Is(err, notify.ErrClosed) {
					// Go: da una goroutine diversa da quella del test si usa t.Errorf, mai
					// t.Fatal: Fatal ferma solo la goroutine che lo chiama, non il test.
					t.Errorf("unexpected error %v", err)
					return
				}
			}
		})
	}
	close(start)
	if err := q.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	wg.Wait()

	s := q.Stats()
	if s.Delivered != s.Published {
		t.Fatalf("delivered %d of %d accepted notifications", s.Delivered, s.Published)
	}
}

func TestShutdownDrainsPendingNotifications(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	q := newQueue(t, 3, 1, func(ctx context.Context, _ notify.Notification) error {
		started <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	q.Start(t.Context())
	for i := range 3 {
		if err := q.TryPublish(notify.Notification{TicketID: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	<-started // il worker sta elaborando la prima; due restano in coda

	shutdownErr := make(chan error, 1)
	// Go: Shutdown blocca finché i worker non finiscono, e i worker aspettano
	// release: lo lanciamo in una goroutine, altrimenti il test non arriverebbe
	// mai a close(release) (deadlock).
	go func() { shutdownErr <- q.Shutdown(t.Context()) }()
	close(release)

	if err := <-shutdownErr; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if s := q.Stats(); s.Delivered != 3 {
		t.Fatalf("delivered %d, want 3 (pending items must be drained)", s.Delivered)
	}
}

// synctest usa un orologio virtuale: la deadline di 5s scatta subito,
// in modo deterministico, senza sleep reali.
//
// Go: testing/synctest (Go 1.25+) esegue la funzione in una "bolla" con un
// orologio finto: il tempo avanza solo quando tutte le goroutine della bolla
// sono bloccate. synctest.Wait aspetta proprio quel momento.
func TestShutdownDeadlineCancelsWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stopped := make(chan error, 1)
		q := newQueue(t, 1, 1, func(ctx context.Context, _ notify.Notification) error {
			<-ctx.Done() // un handler bloccato che rispetta solo la cancellazione
			// Fermarsi richiede un po' di lavoro (orologio finto di synctest):
			// se Shutdown non aspetta i worker, ritorna prima che finisca.
			time.Sleep(100 * time.Millisecond)
			stopped <- ctx.Err()
			return ctx.Err()
		})
		// Background non scade mai: i worker si possono fermare solo con la
		// cancellazione che Shutdown fa alla deadline, che è ciò che il test verifica.
		q.Start(context.Background())
		if err := q.TryPublish(notify.Notification{TicketID: 1}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait() // il worker è dentro l'handler

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := q.Shutdown(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Shutdown = %v, want DeadlineExceeded", err)
		}
		// Niente synctest.Wait qui: Shutdown ritorna solo dopo che il worker
		// è uscito, quindi il fallimento deve essere già contato.
		// Errorf e non Fatalf: il test deve arrivare a <-stopped, altrimenti
		// il worker resta nella bolla di synctest e il test va in panic.
		if s := q.Stats(); s.Failed != 1 {
			t.Errorf("Failed = %d right after Shutdown, want 1 (Shutdown must wait for the workers)", s.Failed)
		}
		if err := <-stopped; !errors.Is(err, context.Canceled) {
			t.Fatalf("handler saw %v, want context.Canceled", err)
		}
	})
}

func TestWorkersStopWhenStartContextIsCancelled(t *testing.T) {
	q := newQueue(t, 1, 3, func(context.Context, notify.Notification) error { return nil })
	// Go: WithCancel invece di WithTimeout: il test decide quando cancellare,
	// chiamando cancel().
	ctx, cancel := context.WithCancel(t.Context())
	q.Start(ctx)
	cancel()

	// I worker sono già usciti: Shutdown ritorna senza attendere la deadline.
	sctx, scancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer scancel()
	if err := q.Shutdown(sctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestLogHandlerRespectsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := notify.LogHandler(discardLogger(), time.Hour)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := h(ctx, notify.Notification{}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("LogHandler = %v, want DeadlineExceeded", err)
		}
	})
}
