-- 0002_tickets: ticket e audit trail.

CREATE TABLE tickets (
    id          BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title       TEXT        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'open'
                            CHECK (status IN ('open', 'in_progress', 'resolved')),
    -- RESTRICT: un utente con ticket aperti non si cancella per sbaglio.
    reporter_id UUID        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- SET NULL: se l'agent viene cancellato il ticket resta senza assegnatario
    -- e un altro agent può farne claim. Lo status non cambia.
    assignee_id UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Lista del requester: WHERE reporter_id = $1 ORDER BY created_at DESC.
CREATE INDEX tickets_reporter_created_idx ON tickets (reporter_id, created_at DESC);
-- Coda dell'agent filtrata per stato.
CREATE INDEX tickets_status_created_idx ON tickets (status, created_at DESC);
-- Ticket assegnati a un agent (indice parziale: i NULL non servono).
CREATE INDEX tickets_assignee_idx ON tickets (assignee_id) WHERE assignee_id IS NOT NULL;

CREATE TABLE ticket_events (
    id         BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- RESTRICT: l'audit non deve sparire implicitamente insieme al ticket.
    ticket_id  BIGINT      NOT NULL REFERENCES tickets (id) ON DELETE RESTRICT,
    actor_id   UUID        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    kind       TEXT        NOT NULL CHECK (kind IN ('created', 'claimed', 'status_changed')),
    payload    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- GET /v1/tickets/{id}/events: WHERE ticket_id = $1 ORDER BY created_at, id.
CREATE INDEX ticket_events_ticket_idx ON ticket_events (ticket_id, created_at, id);
