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
	// Go: WithCancel deriva da ctx un context figlio e restituisce la
	// funzione che lo cancella. La salviamo in q.cancel: la userà Shutdown se
	// la deadline scade. Cancellare il padre cancella anche il figlio, non
	// il contrario.
	ctx, q.cancel = context.WithCancel(ctx)

	// Go: "range q.workers" su un intero (Go 1.22+) conta da 0 a workers-1.
	// wg.Go (Go 1.25+) avvia la funzione in una goroutine e fa da solo
	// wg.Add(1) prima e wg.Done() alla fine. Dalla 1.22 ogni iterazione ha la
	// sua variabile id, quindi ogni closure vede il proprio valore.
	for id := range q.workers {
		q.wg.Go(func() { q.work(ctx, id) })
	}
}

// work è il ciclo di un worker: prende notifiche dalla coda finché la coda
// non è chiusa e vuota, oppure finché ctx non viene cancellato.
func (q *Queue) work(ctx context.Context, id int) {
	for {
		// Go: select aspetta il primo case pronto. Se più case sono pronti
		// insieme ne sceglie uno a caso. Un semplice "for n := range q.ch"
		// non si accorgerebbe mai della cancellazione di ctx.
		select {
		case <-ctx.Done():
			return
		// Go: la ricezione con due valori distingue i due casi: ok è true se
		// n è un valore vero, false se il channel è chiuso e il buffer è già
		// vuoto (allora n è il valore zero di Notification).
		case n, ok := <-q.ch:
			if !ok {
				return // coda chiusa e svuotata
			}
			q.deliver(ctx, id, n)
		}
	}
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
	// Go: read lock + defer unlock. Più TryPublish possono girare in
	// parallelo; Shutdown, che prende il Lock esclusivo, aspetta che escano
	// tutti prima di chiudere il channel.
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return ErrClosed // inviare su un channel chiuso sarebbe un panic
	}
	// Go: un select con "default" non blocca mai. Se l'invio è possibile
	// (c'è posto nel buffer) esegue quel case, altrimenti esegue subito
	// default. È il modo idiomatico di fare un "prova a inviare".
	select {
	case q.ch <- n:
		q.published.Add(1)
		return nil
	default:
		q.dropped.Add(1)
		return ErrQueueFull
	}
}

// Shutdown smette di accettare notifiche, lascia che i worker svuotino la
// coda e li attende. Se ctx scade prima, interrompe i worker, aspetta che
// escano e restituisce un errore che avvolge ctx.Err(). Quando Shutdown
// ritorna nessun worker è più attivo. È sicuro chiamarlo più volte.
func (q *Queue) Shutdown(ctx context.Context) error {
	// Go: qui niente defer: il lock serve solo per queste righe, non durante
	// l'attesa dei worker. Chiudere due volte un channel è un panic: il flag
	// closed rende Shutdown idempotente. Dopo close i worker ricevono
	// ancora quello che resta nel buffer, poi ok == false.
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
	q.mu.Unlock()

	// Go: wg.Wait blocca e non ha un timeout. Per poterlo mettere in un
	// select contro la deadline lo spostiamo in una goroutine che, finita
	// l'attesa, chiude done. chan struct{} non trasporta dati (struct{} occupa
	// zero byte): serve solo come segnale, e chiuderlo "sveglia" chi riceve.
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if q.cancel != nil {
			q.cancel() // i worker vedono ctx.Done() e terminano
		}
		// Anche dopo la deadline si aspetta che i worker escano: chi chiama
		// Shutdown (main) chiude subito dopo il pool del database, che un
		// handler potrebbe ancora usare. L'attesa è breve perché gli handler
		// devono rispettare la cancellazione di ctx (contratto di Handler).
		<-done
		// Go: len su un channel bufferizzato dice quanti elementi aspettano
		// nel buffer in questo istante: qui, le notifiche mai consegnate.
		return fmt.Errorf("notify: shutdown interrupted with %d notifications pending: %w", len(q.ch), ctx.Err())
	}
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
