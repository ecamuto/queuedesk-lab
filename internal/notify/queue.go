// Package notify simula l'invio asincrono di notifiche con un worker pool
// in memoria alimentato da una coda limitata (bounded channel).
//
// Garanzie e limiti:
//   - TryPublish non blocca mai: se la coda è piena restituisce ErrQueueFull
//     e la notifica viene scartata (best effort). Il chiamante ha già
//     committato la transazione e non deve annullarla.
//   - Il numero di goroutine è fisso (N worker): nessuna goroutine per evento.
//   - Shutdown chiude la coda senza rischio di send-on-closed-channel,
//     lascia ai worker il tempo di svuotarla e li attende fino alla deadline.
//   - La coda vive in memoria: se il processo termina in modo anomalo (crash,
//     kill -9) le notifiche in coda vanno perse. Per garanzie at-least-once
//     servirebbe una outbox su PostgreSQL (vedi BACKLOG.md).
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Notification descrive un evento da comunicare a un utente.
//
// Go: è una struct di soli valori (niente puntatori): passarla su un channel
// ne copia il contenuto, quindi produttore e worker non condividono memoria.
type Notification struct {
	Kind        string // created, claimed, status_changed
	TicketID    int64
	ActorID     string // chi ha causato l'evento
	RecipientID string // chi deve essere avvisato (il reporter del ticket)
	Status      string // stato del ticket dopo l'evento
}

// Handler consegna una notifica. Deve rispettare la cancellazione di ctx:
// Shutdown, scaduta la deadline, cancella ctx e aspetta che l'handler esca.
//
// Go: "type Handler func(...)" dà un nome a un tipo funzione. Qualunque
// funzione con questa firma è un Handler: nei test se ne passa una che
// registra le chiamate, in main LogHandler (in fondo al file).
type Handler func(ctx context.Context, n Notification) error

// Go: errori "sentinella", cioè valori fissi creati una volta sola. Il
// chiamante li riconosce con errors.Is(err, notify.ErrQueueFull).
var (
	// ErrQueueFull indica che la notifica è stata scartata perché la coda è piena.
	ErrQueueFull = errors.New("notify: queue full")
	// ErrClosed indica che la coda è in chiusura e non accetta nuove notifiche.
	ErrClosed = errors.New("notify: queue closed")
)

// Stats sono contatori cumulativi, utili nei log e nei test.
type Stats struct {
	Published uint64 // accettate in coda
	Dropped   uint64 // scartate per coda piena
	Delivered uint64 // consegnate con successo
	Failed    uint64 // handler in errore (o interrotto dalla deadline)
}

// Queue è un worker pool con coda limitata.
type Queue struct {
	// Go: "chan Notification" è un channel, un tubo tipizzato fra goroutine.
	// Creato con make(chan T, size) ha un buffer: l'invio non blocca finché
	// ci sono posti liberi, la ricezione blocca finché il buffer è vuoto.
	ch      chan Notification
	workers int
	handle  Handler
	log     *slog.Logger

	// mu protegge closed e la chiusura di ch: i produttori inviano con il
	// read lock, Shutdown chiude con il write lock. Così nessun invio può
	// avvenire su un channel già chiuso.
	//
	// Go: un sync.RWMutex ha due modalità. RLock può essere preso da molte
	// goroutine insieme (lettori); Lock è esclusivo e aspetta che tutti i
	// lettori abbiano rilasciato. Il valore zero è un mutex pronto all'uso:
	// non serve inizializzarlo in New. Non va mai copiato dopo l'uso, per
	// questo Queue si passa sempre come puntatore (*Queue).
	mu     sync.RWMutex
	closed bool

	// Go: un sync.WaitGroup conta le goroutine in corso; Wait blocca finché
	// il contatore torna a zero. cancel è la funzione che cancella il
	// context dei worker (la crea context.WithCancel in Start).
	wg     sync.WaitGroup
	cancel context.CancelFunc

	// Go: atomic.Uint64 è un contatore che più goroutine possono
	// incrementare (Add) e leggere (Load) insieme senza mutex e senza data
	// race. Con un uint64 normale e "x++" il race detector segnalerebbe
	// l'accesso concorrente. Più campi dello stesso tipo si dichiarano
	// insieme separati da virgole.
	published, dropped, delivered, failed atomic.Uint64
}

// New crea una coda con capacità size servita da workers goroutine.
// I worker partono solo con Start.
//
// Go: si valida in New e si restituisce un error, così una *Queue esistente
// è sempre utilizzabile. È il ruolo che in altri linguaggi ha il costruttore.
func New(size, workers int, h Handler, log *slog.Logger) (*Queue, error) {
	if size < 1 || workers < 1 {
		return nil, fmt.Errorf("notify: size and workers must be >= 1 (got %d, %d)", size, workers)
	}
	if h == nil {
		return nil, errors.New("notify: nil handler")
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	// Go: i campi non indicati (mu, wg, closed, contatori) partono dal valore
	// zero, che per tutti questi tipi è già lo stato iniziale corretto.
	return &Queue{ch: make(chan Notification, size), workers: workers, handle: h, log: log}, nil
}

// Start avvia i worker. Va chiamato una sola volta. Se ctx viene cancellato
// i worker terminano subito, senza svuotare la coda: per uno shutdown
// ordinato usare Shutdown e passare qui un context che non scade al segnale.
func (q *Queue) Start(ctx context.Context) {
	// TODO(NOTIFY-2, passo A) — Avviare esattamente q.workers goroutine.
	//
	// Dove serve: main chiama Start una sola volta all'avvio (NOTIFY-5); i
	// test lo chiamano prima di pubblicare.
	//
	//   1. ctx, q.cancel = context.WithCancel(ctx): un context figlio che
	//      Shutdown potrà cancellare con q.cancel quando scade la deadline. Con
	//      "=" e non ":=": q.cancel è un campo, non una variabile nuova;
	//   2. for id := range q.workers { q.wg.Go(func() { q.work(ctx, id) }) }.
	//      sync.WaitGroup.Go (Go 1.25) fa Add prima di avviare la goroutine e
	//      Done quando termina; da Go 1.22 ogni iterazione ha il suo id.
	//
	// Il numero di goroutine è fisso: nessuna goroutine per notifica.
	// Fatto quando: si verifica insieme a NOTIFY-3 (vedi Shutdown).
}

// work è il ciclo di un worker: prende notifiche dalla coda finché la coda
// non è chiusa e vuota, oppure finché ctx non viene cancellato.
func (q *Queue) work(ctx context.Context, id int) {
	// TODO(NOTIFY-2, passo B) — Il ciclo di un worker.
	//
	// Un for senza condizione con dentro un select su due casi:
	//   - <-ctx.Done(): cancellazione (deadline di Shutdown scaduta, oppure
	//     ctx di Start cancellato) → return, anche se la coda non è vuota;
	//   - n, ok := <-q.ch: ok == false vuol dire coda chiusa E vuota →
	//     return; altrimenti q.deliver(ctx, id, n), già scritto qui sotto
	//     (chiama l'handler e aggiorna delivered o failed).
	//
	// Dopo close(q.ch) il channel restituisce ancora tutto ciò che è nel
	// buffer, e solo alla fine ok == false: per questo "chiudere" vuol dire
	// "svuota e poi esci". Un for n := range q.ch da solo non basterebbe:
	// non si accorgerebbe mai della cancellazione.
	// Rimuovi la riga "_, _ = ctx, id".
	// Fatto quando: si verifica insieme a NOTIFY-3 (vedi Shutdown).
	_, _ = ctx, id
}

// deliver chiama l'handler e aggiorna i contatori.
func (q *Queue) deliver(ctx context.Context, worker int, n Notification) {
	if err := q.handle(ctx, n); err != nil {
		q.failed.Add(1)
		q.log.WarnContext(ctx, "notification failed",
			"worker", worker, "kind", n.Kind, "ticket_id", n.TicketID, "err", err)
		return
	}
	q.delivered.Add(1)
}

// TryPublish accoda n senza bloccare. Restituisce ErrQueueFull se la coda
// è piena, ErrClosed se Shutdown è già stato chiamato.
func (q *Queue) TryPublish(n Notification) error {
	// TODO(NOTIFY-1) — Invio non bloccante e sicuro rispetto alla chiusura.
	//
	// Dove serve: ticket.Service.publish chiama TryPublish dopo ogni create,
	// claim o cambio di stato riuscito (NOTIFY-4). Chi chiama ha già fatto
	// commit e sta rispondendo a una richiesta HTTP: non deve mai aspettare.
	//
	//   1. q.mu.RLock() e defer q.mu.RUnlock(): più produttori inviano in
	//      parallelo fra loro, ma nessuno mentre Shutdown tiene il Lock
	//      esclusivo per chiudere q.ch;
	//   2. q.closed → return ErrClosed: inviare su un channel chiuso è panic;
	//   3. invio non bloccante, con select e default:
	//        select {
	//        case q.ch <- n: // c'è posto nel buffer
	//            q.published.Add(1); return nil
	//        default:        // buffer pieno: si scarta, non si aspetta
	//            q.dropped.Add(1); return ErrQueueFull
	//        }
	//
	// Niente goroutine per singola notifica e niente invio bloccante: un
	// q.ch <- n da solo si fermerebbe a coda piena. Rimuovi lo stub.
	// Fatto quando: make test-acceptance RUN=TestTryPublishWhenFullDropsWithoutBlocking
	// (non avvia worker: è il solo test del package che regge da solo). Non
	// lanciare ancora l'intero package: senza NOTIFY-2 e NOTIFY-3 un test
	// resta bloccato fino al timeout, o va in panic (vedi TAPPA.md).
	return fmt.Errorf("TODO(NOTIFY-1) TryPublish: %w", errors.ErrUnsupported)
}

// Shutdown smette di accettare notifiche, lascia che i worker svuotino la
// coda e li attende. Se ctx scade prima, interrompe i worker, aspetta che
// escano e restituisce un errore che avvolge ctx.Err(). Quando Shutdown
// ritorna nessun worker è più attivo. È sicuro chiamarlo più volte.
func (q *Queue) Shutdown(ctx context.Context) error {
	// TODO(NOTIFY-3) — Chiusura ordinata entro la deadline di ctx.
	//
	// Dove serve: main chiama Shutdown dopo lo shutdown HTTP, con la stessa
	// deadline (NOTIFY-5); appena Shutdown ritorna, main chiude il pool
	// PostgreSQL.
	//
	// Passo A — Chiudere la coda, una volta sola:
	//   q.mu.Lock(); se !q.closed → q.closed = true e close(q.ch);
	//   q.mu.Unlock(). Niente defer: il lock serve solo per queste righe, non
	//   durante l'attesa dei worker. Il flag rende Shutdown idempotente:
	//   chiudere due volte un channel è panic, e il test lo chiama due volte.
	//
	// Passo B — Attendere i worker con una deadline. wg.Wait non ha timeout,
	// quindi lo si sposta in una goroutine che segnala la fine:
	//      done := make(chan struct{})
	//      go func() { q.wg.Wait(); close(done) }()
	//
	// Passo C — select fra fine del lavoro e deadline:
	//   - <-done → return nil: coda svuotata, worker usciti;
	//   - <-ctx.Done() → deadline scaduta:
	//       1. se q.cancel != nil chiamalo (Start potrebbe non essere mai
	//          stato chiamato): i worker vedono ctx.Done() ed escono;
	//       2. <-done: aspetta comunque che escano. È breve, perché gli
	//          Handler devono rispettare ctx, e serve a main, che subito dopo
	//          chiude il pool del database;
	//       3. return fmt.Errorf("notify: shutdown interrupted with %d
	//          notifications pending: %w", len(q.ch), ctx.Err()).
	//
	// Rimuovi "_ = ctx" e il "return nil" provvisorio.
	// Fatto quando: go test -tags acceptance -race ./internal/notify è verde
	// (NOTIFY-1…3 insieme; non serve il database).
	_ = ctx
	return nil
}

// Stats restituisce una fotografia dei contatori.
//
// Go: ogni Load è atomico, ma i quattro non sono letti nello stesso istante:
// per log e test va bene, per una contabilità esatta servirebbe un mutex.
func (q *Queue) Stats() Stats {
	return Stats{
		Published: q.published.Load(),
		Dropped:   q.dropped.Load(),
		Delivered: q.delivered.Load(),
		Failed:    q.failed.Load(),
	}
}

// LogValue implementa slog.LogValuer.
//
// Go: come Config.LogValue (internal/config): basta avere il metodo, senza
// dichiarare l'interfaccia. slog lo chiama quando Stats finisce in un log.
func (s Stats) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Uint64("published", s.Published),
		slog.Uint64("dropped", s.Dropped),
		slog.Uint64("delivered", s.Delivered),
		slog.Uint64("failed", s.Failed),
	)
}

// LogHandler è l'Handler della demo: simula la latenza di un invio reale
// (email, chat...) e scrive un log. Rispetta la cancellazione di ctx.
//
// Go: LogHandler restituisce una funzione (una closure) che si ricorda di
// log e latency. È una "factory": la configurazione si passa una volta qui,
// la funzione restituita ha la firma esatta di Handler.
func LogHandler(log *slog.Logger, latency time.Duration) Handler {
	return func(ctx context.Context, n Notification) error {
		// Go: time.Sleep non si può interrompere. Un Timer espone un channel
		// (t.C) che riceve un valore alla scadenza, quindi si può mettere in
		// un select insieme a ctx.Done(). Stop libera il timer se usciamo
		// prima della scadenza.
		t := time.NewTimer(latency)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
		log.InfoContext(ctx, "notification sent",
			"kind", n.Kind, "ticket_id", n.TicketID, "recipient_id", n.RecipientID, "status", n.Status)
		return nil
	}
}
