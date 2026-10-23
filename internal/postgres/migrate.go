package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Questo file è infrastruttura: in tappa 1 non va modificato, e nemmeno
// capito riga per riga. Applica i file migrations/NNNN_nome.up.sql in ordine,
// ricordando in una tabella (schema_migrations) quali ha già applicato.

// migrationLockID è la chiave dell'advisory lock che impedisce a due
// processi di migrare lo stesso database contemporaneamente.
const migrationLockID = 727_001

// MigrateUp applica in ordine le migrazioni *.up.sql non ancora applicate e
// restituisce le versioni applicate. Ogni migrazione gira in una transazione.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]string, error) {
	versions, err := listVersions(fsys)
	if err != nil {
		return nil, err
	}
	var done []string
	err = withMigrationLock(ctx, pool, func(conn *pgxpool.Conn) error {
		applied, err := appliedVersions(ctx, conn)
		if err != nil {
			return err
		}
		for _, v := range versions {
			if applied[v] {
				continue
			}
			if err := runMigration(ctx, conn, fsys, v+".up.sql",
				`INSERT INTO schema_migrations (version) VALUES ($1)`, v); err != nil {
				return err
			}
			done = append(done, v)
		}
		return nil
	})
	return done, err
}

// MigrateDown annulla le ultime steps migrazioni applicate, dalla più recente.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, steps int) ([]string, error) {
	var done []string
	err := withMigrationLock(ctx, pool, func(conn *pgxpool.Conn) error {
		applied, err := appliedVersions(ctx, conn)
		if err != nil {
			return err
		}
		versions := make([]string, 0, len(applied))
		for v := range applied {
			versions = append(versions, v)
		}
		slices.Sort(versions)
		slices.Reverse(versions)
		for _, v := range versions[:min(steps, len(versions))] {
			if err := runMigration(ctx, conn, fsys, v+".down.sql",
				`DELETE FROM schema_migrations WHERE version = $1`, v); err != nil {
				return err
			}
			done = append(done, v)
		}
		return nil
	})
	return done, err
}

func withMigrationLock(ctx context.Context, pool *pgxpool.Pool, fn func(*pgxpool.Conn) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("migrate: lock: %w", err)
	}
	defer func() {
		// context.WithoutCancel: lo sblocco va eseguito anche se ctx è scaduto.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	return fn(conn)
}

func appliedVersions(ctx context.Context, conn *pgxpool.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read applied versions: %w", err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("migrate: read applied versions: %w", err)
	}
	applied := make(map[string]bool, len(versions))
	for _, v := range versions {
		applied[v] = true
	}
	return applied, nil
}

// runMigration esegue lo script e aggiorna schema_migrations nella stessa
// transazione: o entrambi, o niente.
func runMigration(ctx context.Context, conn *pgxpool.Conn, fsys fs.FS, file, bookkeeping, version string) error {
	script, err := fs.ReadFile(fsys, file)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate %s: begin: %w", file, err)
	}
	defer tx.Rollback(ctx) // no-op dopo Commit

	// Exec senza argomenti usa il simple protocol: più statement in un colpo.
	if _, err := tx.Exec(ctx, string(script)); err != nil {
		return fmt.Errorf("migrate %s: %w", file, err)
	}
	if _, err := tx.Exec(ctx, bookkeeping, version); err != nil {
		return fmt.Errorf("migrate %s: bookkeeping: %w", file, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate %s: commit: %w", file, err)
	}
	return nil
}

// listVersions restituisce le versioni (es. "0001_auth") ordinate.
func listVersions(fsys fs.FS) ([]string, error) {
	files, err := fs.Glob(fsys, "*.up.sql")
	if err != nil {
		return nil, fmt.Errorf("migrate: list files: %w", err)
	}
	versions := make([]string, 0, len(files))
	for _, f := range files {
		versions = append(versions, strings.TrimSuffix(f, ".up.sql"))
	}
	slices.Sort(versions)
	return versions, nil
}
