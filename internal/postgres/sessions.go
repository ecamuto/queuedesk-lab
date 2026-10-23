package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"queuedesk/internal/auth"
)

// SessionRepo implementa auth.SessionStore.
type SessionRepo struct {
	pool *pgxpool.Pool
}

// NewSessionRepo crea il repository.
func NewSessionRepo(pool *pgxpool.Pool) *SessionRepo { return &SessionRepo{pool: pool} }

// CreateSession salva l'hash del token, mai il token.
func (r *SessionRepo) CreateSession(ctx context.Context, s auth.Session) error {
	const q = `
		INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4)`

	// Go: Exec si usa per i comandi che non restituiscono righe. Il primo
	// risultato (il "command tag", es. "INSERT 0 1") qui non serve: "_".
	// pgx converte da solo []byte in BYTEA e time.Time in TIMESTAMPTZ.
	if _, err := r.pool.Exec(ctx, q, s.TokenHash, s.UserID, s.CreatedAt, s.ExpiresAt); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// PrincipalBySession carica l'utente di una sessione valida all'istante now.
// now arriva dal service (orologio iniettabile), non da now() SQL: così la
// scadenza è testabile in modo deterministico.
func (r *SessionRepo) PrincipalBySession(ctx context.Context, tokenHash []byte, now time.Time) (auth.Principal, error) {
	// Una sola query con JOIN: sessione valida e dati dell'utente insieme.
	const q = `
		SELECT u.id, u.email, u.role
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2`

	// Go: &p.Role punta a un auth.Role. pgx sa scrivere una colonna TEXT in
	// qualunque tipo con una string sotto.
	var p auth.Principal
	err := r.pool.QueryRow(ctx, q, tokenHash, now).Scan(&p.UserID, &p.Email, &p.Role)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Inesistente, scaduta o revocata: per lo store è lo stesso caso.
		return auth.Principal{}, auth.ErrNotFound
	case err != nil:
		return auth.Principal{}, fmt.Errorf("select session: %w", err)
	}
	return p, nil
}

// DeleteSession revoca la sessione. Idempotente.
//
// Go: un DELETE che non trova righe non è un errore per Exec: per questo
// l'idempotenza non richiede codice in più.
func (r *SessionRepo) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
