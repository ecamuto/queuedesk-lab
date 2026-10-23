package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"queuedesk/internal/ticket"
)

// ClaimTicket usa un UPDATE condizionale: la condizione assignee_id IS NULL
// viene rivalutata da PostgreSQL dopo aver ottenuto il lock di riga, quindi
// fra N richieste concorrenti solo una aggiorna la riga. Le altre trovano
// zero righe e ricevono ErrAlreadyClaimed. Nessun SELECT preliminare: un
// "controlla e poi aggiorna" in due passi avrebbe una race condition.
//
// Go: i metodi su *TicketRepo sono chiamati da più goroutine insieme (una
// per richiesta HTTP). Va bene perché r.pool è sicuro per l'uso concorrente
// e il metodo non modifica campi di r: ogni chiamata ha la sua transazione.
func (r *TicketRepo) ClaimTicket(ctx context.Context, id int64, agentID string) (ticket.Ticket, error) {
	// Go: Begin prende una connessione dal pool e apre una transazione; tx
	// la rappresenta finché non chiamiamo Commit o Rollback.
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("claim ticket: begin: %w", err)
	}
	// Go: il defer garantisce il Rollback su OGNI return di errore qui sotto,
	// senza ripeterlo in ogni ramo. Dopo un Commit riuscito Rollback non fa
	// nulla (restituisce pgx.ErrTxClosed, che ignoriamo): per questo il suo
	// errore non viene controllato.
	defer tx.Rollback(ctx)

	// Go: QueryRow non restituisce un errore subito: lo restituisce Scan
	// (qui dentro scanTicket). Se la query non produce righe, Scan restituisce
	// pgx.ErrNoRows. $1 e $2 sono parametri: i valori viaggiano separati dal
	// testo SQL, quindi nessuna SQL injection. La backtick permette stringhe
	// su più righe; "+ticketColumns" concatena una costante scritta da noi.
	t, err := scanTicket(tx.QueryRow(ctx, `
		UPDATE tickets
		SET assignee_id = $2, updated_at = now()
		WHERE id = $1 AND assignee_id IS NULL
		RETURNING `+ticketColumns,
		id, agentID))
	// Go: errors.Is e non "err == pgx.ErrNoRows": pgx potrebbe restituire
	// l'errore avvolto, ed errors.Is guarda tutta la catena.
	if errors.Is(err, pgx.ErrNoRows) {
		// Zero righe: il ticket non esiste oppure è già assegnato.
		//
		// Go: "var exists bool" dichiara la variabile al valore zero (false).
		// Scan(&exists) le passa l'indirizzo: Scan scrive lì il risultato.
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tickets WHERE id = $1)`, id).Scan(&exists); err != nil {
			return ticket.Ticket{}, fmt.Errorf("claim ticket: check existence: %w", err)
		}
		if !exists {
			// Errori di dominio restituiti così come sono: il service li
			// avvolge con l'ID, l'HTTP li traduce in 404 e 409.
			return ticket.Ticket{}, ticket.ErrNotFound
		}
		return ticket.Ticket{}, ticket.ErrAlreadyClaimed
	}
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("claim ticket: update: %w", err)
	}

	// Go: insertEvent riceve tx (un pgx.Tx), non il pool: l'evento finisce
	// nella stessa transazione dell'UPDATE. map[string]string{...} è una
	// mappa letterale; insertEvent la serializza in JSON per la colonna JSONB.
	if err := insertEvent(ctx, tx, t.ID, agentID, ticket.EventClaimed,
		map[string]string{"assignee_id": agentID}); err != nil {
		return ticket.Ticket{}, fmt.Errorf("claim ticket: %w", err)
	}
	// Solo qui assegnazione ed evento diventano visibili agli altri, insieme.
	if err := tx.Commit(ctx); err != nil {
		return ticket.Ticket{}, fmt.Errorf("claim ticket: commit: %w", err)
	}
	return t, nil
}

// UpdateStatus usa invece un lock pessimistico (SELECT ... FOR UPDATE):
// legge lo stato corrente, verifica assegnatario e transizione con le regole
// del package ticket, poi aggiorna. Il lock impedisce che lo stato cambi fra
// la verifica e l'UPDATE.
func (r *TicketRepo) UpdateStatus(ctx context.Context, id int64, actorID string, to ticket.Status) (ticket.Ticket, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("update status: begin: %w", err)
	}
	defer tx.Rollback(ctx) // no-op dopo Commit

	// FOR UPDATE blocca la riga fino a Commit o Rollback: un secondo
	// UpdateStatus sullo stesso ticket aspetta qui il suo turno.
	cur, err := scanTicket(tx.QueryRow(ctx, `SELECT `+ticketColumns+` FROM tickets WHERE id = $1 FOR UPDATE`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ticket.Ticket{}, ticket.ErrNotFound
	case err != nil:
		return ticket.Ticket{}, fmt.Errorf("update status: lock ticket: %w", err)
	}
	// Go: AssigneeID è un *string, nil se nessuno ha fatto claim (NULL in
	// SQL). Prima si controlla nil, poi si dereferenzia con *: invertire
	// l'ordine su un puntatore nil sarebbe un panic. || valuta la seconda
	// condizione solo se la prima è falsa, quindi qui è sicuro.
	if cur.AssigneeID == nil || *cur.AssigneeID != actorID {
		return ticket.Ticket{}, ticket.ErrNotAssignee
	}
	// La regola sta nel package ticket, non nello SQL: è testabile senza DB.
	if !ticket.CanTransition(cur.Status, to) {
		// Go: %w avvolge l'errore sentinella, %s aggiunge il contesto:
		// il client legge "status transition not allowed: open -> resolved",
		// errors.Is(err, ticket.ErrInvalidTransition) resta vero.
		return ticket.Ticket{}, fmt.Errorf("%w: %s -> %s", ticket.ErrInvalidTransition, cur.Status, to)
	}

	t, err := scanTicket(tx.QueryRow(ctx, `
		UPDATE tickets SET status = $2, updated_at = now()
		WHERE id = $1
		RETURNING `+ticketColumns,
		id, to))
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("update status: update: %w", err)
	}
	// Go: map[string]ticket.Status: anche i valori di una mappa possono avere
	// un tipo nostro; json.Marshal lo serializza come la string sottostante.
	if err := insertEvent(ctx, tx, id, actorID, ticket.EventStatusChanged,
		map[string]ticket.Status{"from": cur.Status, "to": to}); err != nil {
		return ticket.Ticket{}, fmt.Errorf("update status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ticket.Ticket{}, fmt.Errorf("update status: commit: %w", err)
	}
	return t, nil
}
