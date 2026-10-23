package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"queuedesk/internal/auth"
)

// UserRepo implementa auth.UserStore.
//
// Go: la struct contiene solo il pool. Il repository non ha stato proprio:
// ogni metodo prende una connessione dal pool, la usa e la restituisce.
type UserRepo struct {
	pool *pgxpool.Pool
}

// NewUserRepo crea il repository.
func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

// CreateUser inserisce un utente. L'indice unico su lower(email) trasforma
// le email duplicate in auth.ErrEmailTaken.
func (r *UserRepo) CreateUser(ctx context.Context, u auth.NewUser) (auth.User, error) {
	// Go: una costante locale per la query: si legge in un punto solo e il
	// compilatore impedisce di modificarla per sbaglio.
	const q = `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id, email, role, created_at`

	// Go: Scan riceve i puntatori ai campi (&out.ID, ...) e li riempie
	// nell'ordine delle colonne di RETURNING. Il metodo concatenato su più
	// righe richiede il punto alla fine della riga, non all'inizio: Go
	// inserisce un ";" automatico a fine riga quando la riga sembra completa.
	var out auth.User
	err := r.pool.QueryRow(ctx, q, u.Email, u.PasswordHash, u.Role).
		Scan(&out.ID, &out.Email, &out.Role, &out.CreatedAt)
	switch {
	case isUniqueViolation(err):
		return auth.User{}, auth.ErrEmailTaken // errore di dominio, non SQL
	case err != nil:
		return auth.User{}, fmt.Errorf("insert user: %w", err)
	}
	return out, nil
}

// CredentialsByEmail cerca l'utente ignorando maiuscole/minuscole.
func (r *UserRepo) CredentialsByEmail(ctx context.Context, email string) (auth.Credentials, error) {
	// lower(email) nella WHERE usa l'indice users_email_lower_key: deve
	// essere la stessa espressione dell'indice.
	const q = `
		SELECT id, email, role, created_at, password_hash
		FROM users
		WHERE lower(email) = lower($1)`

	// Go: c.ID e c.Email sono i campi di User incorporato in Credentials:
	// si scrivono come se fossero di Credentials.
	var c auth.Credentials
	err := r.pool.QueryRow(ctx, q, email).
		Scan(&c.ID, &c.Email, &c.Role, &c.CreatedAt, &c.PasswordHash)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return auth.Credentials{}, auth.ErrNotFound
	case err != nil:
		return auth.Credentials{}, fmt.Errorf("select user by email: %w", err)
	}
	return c, nil
}
