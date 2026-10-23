package postgres_test

import (
	"testing"
)

// TestSchemaConstraints verifica che i vincoli della migrazione 0002 siano
// nel database e non solo nel codice Go.
func TestSchemaConstraints(t *testing.T) {
	pool := testDB(t)
	ctx := t.Context()
	u := mustUser(t, pool, "ada@example.com", "requester")

	const (
		checkViolation      = "23514"
		foreignKeyViolation = "23503"
	)
	tests := []struct {
		name string
		sql  string
		// Go: []any perché gli argomenti SQL hanno tipi diversi; nil = nessuno.
		args []any
		want string
	}{
		{"unknown status", `INSERT INTO tickets (title, reporter_id, status) VALUES ('x', $1, 'closed')`, []any{u.ID}, checkViolation},
		{"empty title", `INSERT INTO tickets (title, reporter_id) VALUES ('', $1)`, []any{u.ID}, checkViolation},
		{"unknown reporter", `INSERT INTO tickets (title, reporter_id) VALUES ('x', gen_random_uuid())`, nil, foreignKeyViolation},
		{"unknown assignee", `INSERT INTO tickets (title, reporter_id, assignee_id) VALUES ('x', $1, gen_random_uuid())`, []any{u.ID}, foreignKeyViolation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Go: tt.args... espande la slice negli argomenti variadici di Exec.
			_, err := pool.Exec(ctx, tt.sql, tt.args...)
			if code := pgCode(err); code != tt.want {
				t.Fatalf("got %v (SQLSTATE %q), want SQLSTATE %s", err, code, tt.want)
			}
		})
	}

	t.Run("event kind is constrained", func(t *testing.T) {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO tickets (title, reporter_id) VALUES ('x', $1) RETURNING id`, u.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		_, err := pool.Exec(ctx, `INSERT INTO ticket_events (ticket_id, actor_id, kind) VALUES ($1, $2, 'deleted')`, id, u.ID)
		if code := pgCode(err); code != checkViolation {
			t.Fatalf("got %v (SQLSTATE %q), want %s", err, code, checkViolation)
		}
	})

	t.Run("deleting a reporter is restricted", func(t *testing.T) {
		_, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID)
		if code := pgCode(err); code != foreignKeyViolation {
			t.Fatalf("got %v (SQLSTATE %q), want %s (ON DELETE RESTRICT)", err, code, foreignKeyViolation)
		}
	})

	t.Run("deleting an assignee clears the assignment", func(t *testing.T) {
		agent := mustUser(t, pool, "temp-agent@example.com", "agent")
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO tickets (title, reporter_id, assignee_id) VALUES ('x', $1, $2) RETURNING id`, u.ID, agent.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, agent.ID); err != nil {
			t.Fatalf("delete agent: %v", err)
		}
		var assigned bool
		if err := pool.QueryRow(ctx, `SELECT assignee_id IS NOT NULL FROM tickets WHERE id = $1`, id).Scan(&assigned); err != nil {
			t.Fatal(err)
		}
		if assigned {
			t.Fatal("assignee_id still set (want ON DELETE SET NULL)")
		}
	})

	t.Run("list indexes exist", func(t *testing.T) {
		for _, col := range []string{"reporter_id", "status"} {
			var n int
			err := pool.QueryRow(ctx, `
				SELECT count(*) FROM pg_indexes
				WHERE tablename = 'tickets' AND indexdef LIKE '%(' || $1 || '%'`, col).Scan(&n)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				// Go: Errorf e non Fatalf: si controllano tutte le colonne prima di fermarsi.
				t.Errorf("no index on tickets starting with column %s", col)
			}
		}
	})
}
