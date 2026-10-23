// Package postgres implementa gli store di auth e ticket con pgx/v5 e SQL
// esplicito. I repository restituiscono valori ed errori di dominio: non
// conoscono HTTP e non scrivono risposte.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open crea il pool e verifica subito la connessione: meglio fallire
// all'avvio che alla prima richiesta.
//
// Un pool tiene aperte alcune connessioni e le presta a chi esegue una
// query: le richieste HTTP concorrenti le condividono senza riaprirle ogni
// volta. *pgxpool.Pool è sicuro da usare da più goroutine insieme.
//
// Go: il primo parametro è il context: se scade (o viene cancellato) mentre
// ci si connette, Open si arrende. È la convenzione per ogni funzione che fa
// I/O: ctx sempre per primo. I due risultati (*pgxpool.Pool, error) seguono
// la regola "o un valore valido, o un errore": se err != nil il pool è nil.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// Go: ogni errore viene avvolto con un prefisso che dice DOVE è
		// successo; %w conserva l'errore originale per errors.Is/As.
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	// Le sessioni lavorano in UTC: now() e le conversioni testuali non
	// dipendono dal fuso del server.
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close() // non lasciamo connessioni aperte se restituiamo un errore
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}

// Codici SQLSTATE usati per tradurre gli errori in errori di dominio.
const (
	uniqueViolation = "23505"
)

// isUniqueViolation riconosce un INSERT che viola un indice unico (es.
// email già registrata). Si usa dalla tappa 2.
//
// Go: errors.As cerca nella catena di err un *pgconn.PgError e, se c'è, lo
// mette in pgErr. && valuta la seconda condizione solo se la prima è vera.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
