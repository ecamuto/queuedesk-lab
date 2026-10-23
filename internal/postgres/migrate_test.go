package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"queuedesk/internal/postgres"
	"queuedesk/migrations"
)

// TestMigrationsRoundTrip applica tutte le migrazioni, le annulla tutte e le
// riapplica: così anche i file .down.sql sono verificati, non solo gli .up.sql.
//
// Gira su un database temporaneo, creato e cancellato dal test. Sul database
// di test condiviso non si può: `go test ./...` esegue più package in
// parallelo, e un down cancellerebbe le tabelle sotto i loro piedi.
func TestMigrationsRoundTrip(t *testing.T) {
	pool := tempDB(t)
	ctx := t.Context()

	all, err := postgres.MigrateUp(ctx, pool, migrations.FS)
	if err != nil {
		t.Fatalf("first up: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no migrations applied on an empty database")
	}
	assertTables(t, pool, true)

	// Go: slices.Clone copia la slice prima di Reverse, che la modifica sul
	// posto: all serve ancora intatta per il confronto finale.
	wantDown := slices.Clone(all)
	slices.Reverse(wantDown)
	down, err := postgres.MigrateDown(ctx, pool, migrations.FS, len(all))
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if !slices.Equal(down, wantDown) {
		t.Fatalf("down = %v, want %v (most recent first)", down, wantDown)
	}
	assertTables(t, pool, false)

	again, err := postgres.MigrateUp(ctx, pool, migrations.FS)
	if err != nil {
		t.Fatalf("second up: %v", err)
	}
	if !slices.Equal(again, all) {
		t.Fatalf("second up = %v, want %v", again, all)
	}
	assertTables(t, pool, true)

	// Un up senza migrazioni nuove non fa nulla.
	if none, err := postgres.MigrateUp(ctx, pool, migrations.FS); err != nil || len(none) != 0 {
		t.Fatalf("third up = %v, %v; want nothing to do", none, err)
	}
}

// assertTables controlla che le tabelle dello schema ci siano (want true) o
// siano state tutte rimosse (want false).
func assertTables(t *testing.T, pool *pgxpool.Pool, want bool) {
	t.Helper()
	for _, table := range []string{"users", "sessions", "tickets", "ticket_events"} {
		// Go: to_regclass restituisce NULL se la tabella non esiste; con
		// "IS NOT NULL" la query dà un bool che Scan scrive in exists.
		var exists bool
		if err := pool.QueryRow(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != want {
			t.Fatalf("table %s exists = %v, want %v", table, exists, want)
		}
	}
}

// tempDB crea un database vuoto con un nome unico e lo cancella a fine test.
// Senza TEST_DATABASE_URL, o senza il permesso CREATEDB, il test viene saltato.
func tempDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL not set: skipping PostgreSQL integration test")
	}
	admin, err := postgres.Open(t.Context(), raw)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(admin.Close)

	// Il nome contiene "test", come richiede la protezione di testDB.
	name := fmt.Sprintf("queuedesk_test_migrate_%d", time.Now().UnixNano())
	// Go: CREATE DATABASE non accetta parametri ($1): il nome va nel testo
	// SQL. pgx.Identifier.Sanitize lo mette tra virgolette doppie, come si
	// fa con un identificatore SQL, così non può diventare SQL injection.
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(t.Context(), `CREATE DATABASE `+ident); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42501" { // insufficient_privilege
			t.Skip("the test user cannot CREATE DATABASE: skipping migration round trip")
		}
		t.Fatalf("create temporary database: %v", err)
	}
	// Go: le funzioni di Cleanup girano in ordine inverso (LIFO): questa,
	// registrata dopo admin.Close, gira prima, quando admin è ancora aperto.
	// t.Context() qui è già cancellato, quindi serve un context nuovo.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+ident+` WITH (FORCE)`); err != nil {
			t.Errorf("drop temporary database %s: %v", name, err)
		}
	})

	// Stesso URL, ma con il nome del database temporaneo nel path.
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := postgres.Open(t.Context(), u.String())
	if err != nil {
		t.Fatalf("open temporary database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
