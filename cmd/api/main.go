// Command api avvia il servizio QueueDesk.
//
// main fa solo quattro cose: legge la configurazione, compone le
// dipendenze, avvia il server e lo chiude in modo ordinato.
//
// Go: "package main" con una funzione main() produce un eseguibile.
// `go run ./cmd/api` lo compila e lo avvia; `go build` crea il binario.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"queuedesk/internal/auth"
	"queuedesk/internal/config"
	"queuedesk/internal/httpapi"
	"queuedesk/internal/postgres"
	"queuedesk/internal/ticket"
)

const dbConnectTimeout = 10 * time.Second

// main resta minuscola: crea il logger, chiama run e trasforma un errore in
// exit code 1. Tutta la logica sta in run, che restituisce un error e quindi
// si può testare (vedi main_test.go).
func main() {
	// Log JSON su stdout, una riga per evento; NewLogHandler aggiunge il
	// request_id quando il context lo contiene (middleware.go).
	log := slog.New(httpapi.NewLogHandler(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(log); err != nil {
		log.Error("queuedesk stopped", "err", err)
		// Go: os.Exit termina subito, senza eseguire i defer. Per questo
		// lo chiamiamo qui, fuori da run, dove i defer sono già girati.
		os.Exit(1)
	}
}

// run compone il servizio e blocca finché non arriva un segnale o un errore.
func run(log *slog.Logger) error {
	// Go: os.Getenv è passata come valore (senza parentesi): config.Load la
	// chiamerà per ogni variabile.
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		// Go: niente eccezioni. L'errore si controlla subito e si
		// restituisce al chiamante (main), che decide cosa farne.
		return err
	}
	log.Info("starting queuedesk", "config", cfg) // cfg.LogValue nasconde la password

	// ctx viene cancellato al primo SIGINT (Ctrl+C) o SIGTERM.
	//
	// Go: un context.Context porta una cancellazione (e a volte una
	// scadenza) attraverso le chiamate. context.Background() è la radice
	// vuota. NotifyContext ne deriva uno che viene cancellato quando arriva
	// uno dei segnali; ctx.Done() è un canale che si chiude in quel momento.
	// stop smette di intercettare i segnali: "defer stop()" lo garantisce
	// all'uscita da run.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// La connessione al DB ha 10 secondi, non di più. cancel() libera il
	// timer appena Open ha finito: non serve aspettare la scadenza.
	connectCtx, cancel := context.WithTimeout(ctx, dbConnectTimeout)
	pool, err := postgres.Open(connectCtx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	// Go: i defer girano all'uscita da run in ordine inverso (LIFO):
	// questo, registrato dopo "defer stop()", gira prima di lui.
	defer pool.Close() // ultimo passo: dopo lo shutdown HTTP

	authSvc := auth.NewService(postgres.NewUserRepo(pool), postgres.NewSessionRepo(pool), cfg.SessionTTL, time.Now)
	// TODO(NOTIFY-5) — Collegare la coda di notifiche al servizio.
	//
	// Oggi ticket.NewService riceve nil: nessuna notifica. Nessun test
	// automatico copre questo TODO: si verifica dal vivo (TAPPA.md).
	//
	// Passo A — Creare e avviare la coda, qui:
	//   1. aggiungi l'import "queuedesk/internal/notify";
	//   2. queue, err := notify.New(cfg.NotifyQueueSize, cfg.NotifyWorkers,
	//          notify.LogHandler(log, 200*time.Millisecond), log)
	//      errore → return err. I 200 ms simulano la latenza di un invio vero
	//      (email, chat);
	//   3. queue.Start(context.Background()). NON ctx: ctx viene cancellato al
	//      primo Ctrl+C e i worker si fermerebbero subito invece di svuotare
	//      la coda. Li ferma Shutdown, e solo alla deadline;
	//   4. passa queue al posto di nil in ticket.NewService: *notify.Queue
	//      soddisfa ticket.Publisher senza dichiararlo.
	//
	// Passo B — Chiuderla nello shutdown, nell'ordine giusto:
	//   1. aggiungi il parametro queue *notify.Queue a shutdown e passalo
	//      nella chiamata in fondo a run;
	//   2. in shutdown, DOPO srv.Shutdown(ctx) e con lo stesso ctx, chiama
	//      queue.Shutdown(ctx). Raccogli i due errori e restituiscili con
	//      errors.Join, invece di uscire al primo: la coda va chiusa anche se
	//      l'HTTP ha superato la deadline;
	//   3. log.Info("shutdown complete", "notifications", queue.Stats()).
	//   Perché quest'ordine? Le richieste HTTP ancora in corso possono
	//   pubblicare: la coda deve restare aperta finché non sono finite. Il
	//   pool del DB lo chiude il defer, per ultimo.
	//
	// Fatto quando: make test resta verde (i test di cmd/api avviano davvero
	// run), un ticket creato produce "notification sent" nel log e Ctrl+C
	// stampa "shutdown complete" con le statistiche.
	ticketSvc := ticket.NewService(postgres.NewTicketRepo(pool), nil, log)
	handler := httpapi.NewHandler(httpapi.Deps{Auth: authSvc, Tickets: ticketSvc, DB: pool, Logger: log})
	srv := httpapi.NewServer(cfg.HTTPAddr, handler, log)

	// Go: un canale (chan) è un tubo tipizzato fra goroutine. Con buffer 1
	// la goroutine può depositare l'errore e terminare anche se nessuno lo
	// sta ancora leggendo.
	serveErr := make(chan error, 1)

	// Go: "go f()" avvia f in una goroutine, cioè in parallelo, e prosegue
	// subito. ListenAndServe blocca finché il server vive: se lo chiamassimo
	// qui, run non arriverebbe mai al select che aspetta il segnale.
	go func() {
		log.Info("http server listening", "addr", cfg.HTTPAddr)
		// Dopo srv.Shutdown, ListenAndServe restituisce sempre
		// http.ErrServerClosed: non è un errore, è la chiusura normale.
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err // Go: "<-" invia sul canale
		}
		close(serveErr)
	}()

	// Go: select aspetta il primo fra più canali pronti ed esegue quel case.
	var runErr error
	select {
	case err := <-serveErr: // es. porta già occupata
		runErr = fmt.Errorf("http server: %w", err)
	case <-ctx.Done(): // il canale si chiude al primo Ctrl+C o SIGTERM
		log.Info("shutdown signal received")
	}
	stop() // un secondo Ctrl+C termina subito il processo

	return errors.Join(runErr, shutdown(log, cfg.ShutdownTimeout, srv))
}

// shutdown smette di accettare richieste e attende quelle in corso, entro
// la deadline. Il pool PostgreSQL viene chiuso dal defer in run, per ultimo.
func shutdown(log *slog.Logger, timeout time.Duration, srv *http.Server) error {
	// Un context NUOVO, con la sua deadline: quello di run è già stato
	// cancellato dal segnale, e Shutdown non aspetterebbe nessuno.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	log.Info("shutdown complete")
	return nil
}
