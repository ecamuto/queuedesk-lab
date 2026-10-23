package postgres_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"queuedesk/internal/postgres"
	"queuedesk/migrations"
)

// testDB apre il database indicato da TEST_DATABASE_URL, applica le
// migrazioni e svuota le tabelle. Senza la variabile il test viene saltato,
// così `go test ./...` funziona anche senza PostgreSQL.
//
// Go: questi sono integration test: parlano con un PostgreSQL vero, a
// differenza degli unit test con i fake. t.Skip segna il test come saltato
// (SKIP), non come fallito.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set: skipping PostgreSQL integration test")
	}
	ctx := t.Context()

	pool, err := postgres.Open(ctx, url)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	// Go: t.Cleanup registra una funzione da eseguire a fine test, anche se
	// fallisce. È il "defer" dei test, ma vale anche quando lo chiama un helper:
	// un defer qui chiuderebbe il pool all'uscita di testDB, troppo presto.
	t.Cleanup(pool.Close)

	// Protezione: il TRUNCATE qui sotto non deve mai colpire un DB reale.
	var name string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "test") {
		t.Fatalf("refusing to run destructive tests on database %q (name must contain \"test\")", name)
	}

	if _, err := postgres.MigrateUp(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE ticket_events, tickets, sessions, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return pool
}

// testUser è l'utente creato da mustUser.
// Go: una struct di un solo campo, scritta su una riga.
type testUser struct{ ID string }

// mustUser inserisce un utente con SQL diretto: i test dello schema non
// dipendono dal package auth.
func mustUser(t *testing.T, pool *pgxpool.Pool, email, role string) testUser {
	t.Helper()
	var u testUser
	err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email, password_hash, role) VALUES ($1, 'not-a-real-hash', $2) RETURNING id`,
		email, role).Scan(&u.ID)
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return u
}

// pgCode estrae lo SQLSTATE da un errore PostgreSQL ("" se non lo è).
func pgCode(err error) string {
	// Go: errors.As cerca nella catena un errore di tipo *pgconn.PgError e lo
	// copia in pgErr: da lì si legge il codice SQLSTATE.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
