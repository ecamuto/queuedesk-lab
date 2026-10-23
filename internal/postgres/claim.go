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
	// TODO(CLAIM-1) — Il cuore del laboratorio: un claim con un solo vincitore.
	//
	// Dove serve: POST /v1/tickets/{id}/claim → Service.Claim (CLAIM-2) → qui.
	// Più agent possono arrivare nello stesso istante, da goroutine diverse o
	// da istanze diverse del servizio: la garanzia la dà PostgreSQL, non Go.
	//
	// Domanda guida, prima di scrivere: perché "SELECT assignee_id; se è NULL,
	// UPDATE" non funziona con due agent simultanei? Disegnalo su una linea
	// del tempo: entrambi leggono NULL, entrambi aggiornano, vincono in due.
	//
	// Passo A — Transazione:
	//   1. tx, err := r.pool.Begin(ctx); errore →
	//      fmt.Errorf("claim ticket: begin: %w", err);
	//   2. defer tx.Rollback(ctx): annulla tutto su qualunque return d'errore;
	//      dopo un Commit riuscito non fa nulla;
	//   3. da qui in poi ogni query usa tx, mai r.pool: una query su r.pool
	//      uscirebbe dalla transazione.
	//
	// Passo B — Un solo UPDATE atomico, senza SELECT preliminare:
	//      t, err := scanTicket(tx.QueryRow(ctx, `
	//          UPDATE tickets SET assignee_id = $2, updated_at = now()
	//          WHERE id = $1 AND assignee_id IS NULL
	//          RETURNING `+ticketColumns, id, agentID))
	//   La condizione assignee_id IS NULL sta DENTRO l'UPDATE: chi arriva
	//   secondo aspetta il lock della riga, poi PostgreSQL rivaluta la WHERE
	//   sulla versione aggiornata e non trova nulla da aggiornare. RETURNING
	//   dice subito se hai vinto (una riga) o perso (pgx.ErrNoRows).
	//
	// Passo C — Zero righe ha due cause, da distinguere SOLO adesso:
	//   1. errors.Is(err, pgx.ErrNoRows) → nella stessa tx
	//      SELECT EXISTS (SELECT 1 FROM tickets WHERE id = $1), Scan in un
	//      bool; errore della query → "claim ticket: check existence: %w";
	//   2. false → ticket.ErrNotFound (404); true → ticket.ErrAlreadyClaimed
	//      (409). Restituiscili così come sono: li avvolge il service;
	//   3. ogni altro errore dell'UPDATE → "claim ticket: update: %w".
	//   Prima dell'UPDATE non serve nessuna query: leggere assignee_id e
	//   decidere in Go riaprirebbe la race; un EXISTS preliminare sarebbe solo
	//   una query in più anche per chi vince.
	//
	// Passo D — Audit e commit, solo per il vincitore:
	//   1. insertEvent(ctx, tx, t.ID, agentID, ticket.EventClaimed,
	//      map[string]string{"assignee_id": agentID}); errore →
	//      "claim ticket: %w";
	//   2. tx.Commit(ctx); errore → "claim ticket: commit: %w";
	//   3. return t, nil.
	//
	// Rimuovi lo stub. Gli import "errors" e pgx restano: li usa anche
	// UpdateStatus. Leggi UpdateStatus qui sotto: stesso problema, risolto con
	// un lock pessimistico (SELECT ... FOR UPDATE) invece che con la WHERE.
	// Fatto quando: make test-acceptance RUN='TestClaimConcurrentSingleWinner|TestClaimErrors'
	// (richiede make db-up). Per stanare race intermittenti ripeti il test
	// concorrente con -count=20: il comando è in TAPPA.md.
	return ticket.Ticket{}, fmt.Errorf("TODO(CLAIM-1) ClaimTicket: %w", errors.ErrUnsupported)
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
