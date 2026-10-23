package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"queuedesk/internal/ticket"
)

// TicketRepo implementa ticket.Store.
type TicketRepo struct {
	pool *pgxpool.Pool
}

// NewTicketRepo crea il repository.
func NewTicketRepo(pool *pgxpool.Pool) *TicketRepo { return &TicketRepo{pool: pool} }

// ticketColumns è l'elenco delle colonne nell'ordine atteso da scanTicket.
// Tutte le query sui ticket lo usano, così SELECT e Scan non si disallineano.
const ticketColumns = `id, title, description, status, reporter_id, assignee_id, created_at, updated_at`

// scanTicket legge una riga di ticketColumns in un ticket.Ticket.
//
// Go: pgx.Row è un'interfaccia con il solo metodo Scan. La soddisfano sia
// il risultato di QueryRow sia ogni riga di CollectRows: così una sola
// funzione serve per una riga singola e per le liste.
func scanTicket(row pgx.Row) (ticket.Ticket, error) {
	var t ticket.Ticket
	// Go: &t.AssigneeID è un **string (puntatore a puntatore): pgx ci scrive
	// nil se la colonna è NULL, altrimenti un puntatore alla stringa letta.
	err := row.Scan(&t.ID, &t.Title, &t.Description, &t.Status,
		&t.ReporterID, &t.AssigneeID, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// insertEvent scrive l'audit usando la transazione del chiamante.
//
// Go: payload è any perché ogni evento ha la sua forma (una mappa, una
// struct...): json.Marshal accetta qualunque valore serializzabile. Il
// parametro tx (e non il pool) rende impossibile scrivere l'evento fuori
// dalla transazione del ticket.
func insertEvent(ctx context.Context, tx pgx.Tx, ticketID int64, actorID string, kind ticket.EventKind, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s event: %w", kind, err)
	}
	const q = `INSERT INTO ticket_events (ticket_id, actor_id, kind, payload) VALUES ($1, $2, $3, $4)`
	if _, err := tx.Exec(ctx, q, ticketID, actorID, kind, data); err != nil {
		return fmt.Errorf("insert %s event: %w", kind, err)
	}
	return nil
}

// CreateTicket inserisce ticket ed evento "created" nella stessa transazione.
func (r *TicketRepo) CreateTicket(ctx context.Context, nt ticket.NewTicket) (ticket.Ticket, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("create ticket: begin: %w", err)
	}
	defer tx.Rollback(ctx) // no-op dopo Commit

	// RETURNING restituisce la riga appena inserita, con i valori generati
	// dal database (id, status di default, created_at): niente SELECT dopo.
	t, err := scanTicket(tx.QueryRow(ctx, `
		INSERT INTO tickets (title, description, reporter_id)
		VALUES ($1, $2, $3)
		RETURNING `+ticketColumns,
		nt.Title, nt.Description, nt.ReporterID))
	if err != nil {
		return ticket.Ticket{}, fmt.Errorf("create ticket: insert: %w", err)
	}
	if err := insertEvent(ctx, tx, t.ID, nt.ReporterID, ticket.EventCreated,
		map[string]string{"title": t.Title}); err != nil {
		return ticket.Ticket{}, fmt.Errorf("create ticket: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ticket.Ticket{}, fmt.Errorf("create ticket: commit: %w", err)
	}
	return t, nil
}

// ListTickets costruisce la WHERE in base al filtro. I valori passano sempre
// come parametri ($1, $2...): nella stringa SQL finiscono solo frammenti
// costanti scritti da noi.
func (r *TicketRepo) ListTickets(ctx context.Context, f ticket.ListFilter) ([]ticket.Ticket, error) {
	// Go: un blocco var dichiara più variabili al valore zero: due slice nil,
	// pronte per append. []any perché i parametri hanno tipi diversi.
	var (
		conds []string
		args  []any
	)
	// Il numero del parametro è la posizione in args: dopo l'append,
	// len(args) è proprio il $N appena aggiunto.
	if f.ReporterID != "" {
		args = append(args, f.ReporterID)
		conds = append(conds, fmt.Sprintf("reporter_id = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	q := `SELECT ` + ticketColumns + ` FROM tickets`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, " AND ")
	}
	args = append(args, f.Limit)
	// id DESC come secondo criterio: due ticket con lo stesso created_at
	// hanno comunque un ordine stabile.
	q += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(args))

	// Go: "args..." espande la slice negli argomenti variadici di Query.
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	// Go: CollectRows scorre tutte le righe, chiama la funzione per ognuna,
	// chiude rows e restituisce la slice (o il primo errore). Senza, servono
	// il ciclo rows.Next(), rows.Err() e defer rows.Close(). La funzione
	// anonima adatta scanTicket alla firma richiesta da CollectRows.
	ts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ticket.Ticket, error) {
		return scanTicket(row)
	})
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	return ts, nil
}

// TicketByID restituisce ticket.ErrNotFound se l'ID non esiste.
func (r *TicketRepo) TicketByID(ctx context.Context, id int64) (ticket.Ticket, error) {
	// Nessuna transazione: una sola SELECT è già atomica.
	t, err := scanTicket(r.pool.QueryRow(ctx, `SELECT `+ticketColumns+` FROM tickets WHERE id = $1`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ticket.Ticket{}, ticket.ErrNotFound
	case err != nil:
		return ticket.Ticket{}, fmt.Errorf("select ticket %d: %w", id, err)
	}
	return t, nil
}

// ListEvents restituisce l'audit in ordine cronologico.
func (r *TicketRepo) ListEvents(ctx context.Context, ticketID int64) ([]ticket.Event, error) {
	const q = `
		SELECT id, ticket_id, actor_id, kind, payload, created_at
		FROM ticket_events
		WHERE ticket_id = $1
		ORDER BY created_at, id`

	rows, err := r.pool.Query(ctx, q, ticketID)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	evs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ticket.Event, error) {
		// Go: &e.Payload è un *json.RawMessage: pgx ci copia i byte della
		// colonna JSONB senza decodificarli.
		var e ticket.Event
		err := row.Scan(&e.ID, &e.TicketID, &e.ActorID, &e.Kind, &e.Payload, &e.CreatedAt)
		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return evs, nil
}
