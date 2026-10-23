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

	"queuedesk/internal/config"
	"queuedesk/internal/httpapi"
	"queuedesk/internal/postgres"
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

	// Go: pool è un *pgxpool.Pool, ma il campo DB è di tipo httpapi.Pinger.
	// Compila perché *pgxpool.Pool ha il metodo Ping(context.Context) error:
	// l'interfaccia è soddisfatta implicitamente.
	handler := httpapi.NewHandler(httpapi.Deps{DB: pool, Logger: log})
	srv := httpapi.NewServer(cfg.HTTPAddr, handler, log)

	// TODO(BASE-4, passo A) — Avviare il server senza bloccare run.
	//
	// Problema: srv.ListenAndServe() blocca. Se lo chiami direttamente, run non
	// arriva al codice che ascolta Ctrl+C. Servono due flussi concorrenti:
	//
	//   goroutine server ──errore──▶ serveErr ─┐
	//                                         ├─▶ select ─▶ shutdown
	//   SIGINT/SIGTERM ───────────▶ ctx.Done ─┘
	//
	// Implementazione, in ordine:
	//   1. crea serveErr := make(chan error, 1); buffer 1 evita che la goroutine
	//      resti bloccata se arriva contemporaneamente un segnale;
	//   2. avvia una goroutine che registra "http server listening" e chiama
	//      srv.ListenAndServe();
	//   3. se l'errore NON è http.ErrServerClosed, invialo su serveErr;
	//   4. chiudi serveErr quando la goroutine termina;
	//   5. nel flusso principale usa select: errore da serveErr oppure segnale
	//      da ctx.Done(). Salva l'eventuale errore in runErr con prefisso
	//      "http server:"; sul segnale registra "shutdown signal received";
	//   6. dopo il select chiama stop();
	//   7. restituisci errors.Join(runErr,
	//      shutdown(log, cfg.ShutdownTimeout, srv)).
	//
	// Rimuovi entrambe le righe provvisorie sotto. errors.Join ignora i nil.
	// Fatto quando: TestRunGracefulShutdown e TestRunPortInUse passano.
	_ = srv
	return fmt.Errorf("TODO(BASE-4) serve: %w", errors.ErrUnsupported)
}

// shutdown smette di accettare richieste e attende quelle in corso, entro
// la deadline. Il pool PostgreSQL viene chiuso dal defer in run, per ultimo.
func shutdown(log *slog.Logger, timeout time.Duration, srv *http.Server) error {
	// TODO(BASE-4, passo B) — Chiudere il server entro una deadline.
	//
	// Il context di run è già cancellato dal segnale: non riusarlo. Crea un
	// context nuovo da context.Background(), con durata timeout.
	//
	// Poi:
	//   1. defer cancel();
	//   2. chiama srv.Shutdown(ctx);
	//   3. se fallisce, restituisci fmt.Errorf("http shutdown: %w", err);
	//   4. altrimenti registra "shutdown complete" e restituisci nil.
	//
	// Rimuovi entrambe le righe provvisorie sotto.
	// Fatto quando: Ctrl+C chiude con exit 0 entro SHUTDOWN_TIMEOUT.
	_, _, _ = log, timeout, srv
	return fmt.Errorf("TODO(BASE-4) shutdown: %w", errors.ErrUnsupported)
}
