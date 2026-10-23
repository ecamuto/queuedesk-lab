# QueueDesk — laboratorio Go

Servizio REST per un piccolo help desk interno. I **requester** si registrano, fanno login,
aprono ticket e vedono solo i propri. Gli **agent** vedono la coda, prendono in carico un ticket
(*claim*) e ne aggiornano lo stato. Ogni mutazione scrive un evento di audit nella stessa
transazione; dopo il commit una notifica viene accodata a un worker pool in memoria.

Stack: Go 1.27, `net/http` con il `ServeMux` a pattern, PostgreSQL 17 con `pgx/v5` e SQL esplicito,
`golang.org/x/crypto/bcrypt`. Nessun'altra dipendenza.

> **Sei in un branch di tappa?** Le istruzioni della tappa corrente sono in [`TAPPA.md`](TAPPA.md).
> Questo README descrive il servizio completo e vale per tutti i branch.

---

## Come funziona il laboratorio

Il progetto cresce in sei tappe, una per branch. Ogni tappa contiene **la soluzione delle tappe
precedenti** e solo il codice che serve fino a quel momento: i package delle tappe successive non
esistono ancora.

| Branch | Giorno | Tappa | Cosa si costruisce |
|---|---|---|---|
| `tappa-1-setup` | 1 | Fondamenta del servizio | configurazione, health check, middleware, avvio e shutdown |
| `tappa-2-auth` | 1 | Autenticazione | token opaco, login, sessioni, middleware 401/403 |
| `tappa-3-ticket` | 1 | API ticket | creazione, validazione, lista, regole di visibilità |
| `tappa-4-claim` | 2 | Claim e transazioni | claim concorrente con un solo vincitore, stati, errori → HTTP |
| `tappa-5-notify` | 2 | Worker pool | coda limitata, worker, shutdown; integrazione |
| `tappa-6-robustezza` | 2 | Robustezza | JSON rigoroso, race detector, profiling, demo |
| `soluzione` | — | — | il servizio completo (pubblicato dal docente a fine corso) |

### Flusso di lavoro con git

```bash
# inizio di una tappa: un branch tuo che parte da quello ufficiale
git switch -c lavoro/tappa-1 origin/tappa-1-setup

# durante la tappa: lavora e fai commit quando vuoi
git commit -am "BASE-1: configurazione"

# tappa successiva: riparti dalla versione ufficiale, che contiene la
# soluzione della tappa appena chiusa (il tuo lavoro resta nel tuo branch)
git switch -c lavoro/tappa-2 origin/tappa-2-auth
```

Per confrontare la tua soluzione con quella ufficiale:

```bash
git diff origin/tappa-1-setup lavoro/tappa-1         # cosa hai cambiato tu
git diff origin/tappa-1-setup origin/tappa-2-auth -- internal/config/   # la soluzione ufficiale di BASE-1
```

Se cambi branch con modifiche non salvate, git si ferma: fai prima `git commit` o `git stash`.

### Test baseline e acceptance

- `make test`: test **baseline**, già verdi; devono restare verdi.
- `make test-acceptance`: test della **tappa corrente** (file con `//go:build acceptance` in testa).
  Sono rossi all'inizio e diventano verdi man mano che completi i TODO. Non modificarli: sono il
  contratto. Filtra con `make test-acceptance RUN=TestNome`.
- `make todo`: elenco dei TODO rimasti, con file e riga.
- Una parte non ancora implementata risponde **501 `not_implemented`** con l'ID del TODO.

---

## Requisiti

| Strumento | Versione | Note |
|---|---|---|
| Go | 1.27.2+ | `go.mod` fissa `toolchain go1.27.2` (patch di sicurezza di `net/http` e `crypto/tls`). Con un Go più vecchio (≥ 1.21) e `GOTOOLCHAIN=auto` il toolchain viene scaricato da solo. |
| Docker + Compose v2 | recente | Solo per PostgreSQL (`compose.yaml`). Serve `docker compose up --wait`. |
| git, curl, bash | qualsiasi | `scripts/smoke.sh` funziona anche con il bash 3.2 di macOS. |

## Avvio rapido

```bash
cp .env.example .env     # configurazione locale (il Makefile la carica)
make db-up               # PostgreSQL su localhost:55432 (+ database queuedesk_test)
make migrate-up          # crea lo schema
make seed                # agent demo agent@queuedesk.local (dalla tappa 2)
make run                 # API su :8080 (Ctrl+C = shutdown ordinato)
make smoke               # demo end-to-end (dalla tappa 6, in un altro terminale)
```

## Configurazione

Le variabili sono lette e validate all'avvio da `internal/config`. Se una o più sono errate il
processo termina subito e le elenca **tutte** insieme.

| Variabile | Default | Vincoli |
|---|---|---|
| `DATABASE_URL` | — (obbligatoria) | schema `postgres://` o `postgresql://`, host presente |
| `HTTP_ADDR` | `:8080` | formato `host:porta`; porta numerica (0–65535) o nome di servizio (`:http`) |
| `SESSION_TTL` | `24h` | durata Go, tra `1m` e `720h` |
| `SHUTDOWN_TIMEOUT` | `10s` | tra `1s` e `5m` |
| `NOTIFY_WORKERS` | `4` | intero tra 1 e 64 |
| `NOTIFY_QUEUE_SIZE` | `64` | intero tra 1 e 10000 |

`SEED_AGENT_EMAIL` e `SEED_AGENT_PASSWORD` servono solo a `make seed` e allo smoke test. La password
di `DATABASE_URL` non compare mai nei log: `Config` implementa `slog.LogValuer`.

## Migrazioni e seed

Le migrazioni sono file SQL in `migrations/`, incorporati nel binario con `embed`. `cmd/migrate` le
applica in ordine, ciascuna in una transazione, con un advisory lock e la tabella `schema_migrations`.

```bash
make migrate-up      # applica le mancanti
make migrate-down    # annulla l'ultima
make migrate-redo    # down+up dell'ultima, su DB di sviluppo e di test
```

**Agent demo.** `/v1/auth/register` crea *sempre* un requester. Un agent si ottiene solo con il seed
esplicito (`make seed`, oppure `SEED_AGENT_PASSWORD=... go run ./cmd/seed -email ...`). Se l'email
esiste già il seed non modifica nulla: non promuove mai un requester esistente.

## Comandi make

| Comando | Cosa fa |
|---|---|
| `make help` | elenco dei target disponibili nella tappa |
| `make db-up` / `db-down` / `db-reset` / `db-psql` | gestione di PostgreSQL |
| `make migrate-up` / `migrate-down` / `migrate-redo` | migrazioni |
| `make seed` | agent demo |
| `make run` / `make build` | avvia o compila l'API |
| `make fmt` / `make vet` / `make test` / `make test-race` | qualità di base |
| `make test-acceptance [RUN=…]` | acceptance test della tappa |
| `make test-integration` / `make test-all` | integration test PostgreSQL / tutto |
| `make cover` | report di copertura |
| `make check` | `fmt` + `vet` + `test-race` |
| `make todo` | TODO rimasti |
| `make smoke` | demo curl end-to-end |

## API (servizio completo)

Le rotte compaiono tappa dopo tappa: health nella 1, auth nella 2, ticket nella 3, claim e stato
nella 4. Gli errori usano sempre l'envelope:

```json
{"error":{"code":"already_claimed","message":"ticket already claimed by another agent"}}
```

| Metodo e path | Chi | Successo | Errori principali |
|---|---|---|---|
| `GET /healthz` | tutti | 200 `{"status":"ok"}` | — (non tocca il DB) |
| `GET /readyz` | tutti | 200 `{"status":"ready"}` | 503 `not_ready` (ping DB, timeout 2s) |
| `POST /v1/auth/register` | tutti | 201 utente (ruolo `requester`) | 400 `invalid_input`, 409 `email_taken` |
| `POST /v1/auth/login` | tutti | 200 `{token, token_type, expires_at}` | 401 `invalid_credentials` |
| `DELETE /v1/auth/session` | autenticato | 204 | 401 |
| `GET /v1/me` | autenticato | 200 `{id, email, role}` | 401 |
| `POST /v1/tickets` | autenticato | 201 + `Location` | 400 `invalid_input` |
| `GET /v1/tickets?status=&limit=` | autenticato | 200 `{"tickets":[...]}` | 400 `invalid_input` |
| `GET /v1/tickets/{id}` | owner o agent | 200 | 400 `invalid_id`, 404 `not_found` |
| `POST /v1/tickets/{id}/claim` | agent | 200 | 403 `forbidden`, 404, 409 `already_claimed` |
| `PATCH /v1/tickets/{id}/status` | agent assegnato | 200 | 403 `forbidden` / `not_assignee`, 404, 409 `invalid_transition` |
| `GET /v1/tickets/{id}/events` | owner o agent | 200 `{"events":[...]}` | 404 |

Regole comuni a tutti i body (dalla tappa 6): `Content-Type: application/json` obbligatorio (415),
massimo 64 KiB (413), campi sconosciuti rifiutati (400 `unknown_field`), un solo valore JSON (400).

- **401 contro 403.** 401 `unauthenticated` (con `WWW-Authenticate: Bearer`): header assente o
  malformato, token sconosciuto, scaduto o revocato. 403: sei autenticato ma non autorizzato.
- **Visibilità.** Un agent vede tutto. Un requester vede solo i propri ticket; per un ticket altrui
  riceve **404**, non 403, così non può scoprire quali ID esistono.
- **Stati.** `open → in_progress → resolved`, più `in_progress → open` e `resolved → in_progress`.
  Ogni altra transizione dà 409 `invalid_transition`. Il claim assegna il ticket ma non ne cambia
  lo stato, e nessun cambio di stato tocca l'assegnatario: un ticket riportato a `open` resta
  dell'agent (è "in attesa"), e un claim di un altro agent risponde 409 `already_claimed`.
- **Lista.** `status` vale `open`, `in_progress` o `resolved`; `limit` va da 1 a 100 (default 20).
  L'ordine è dal più recente.

## Esempi curl

```bash
BASE=http://localhost:8080; JSON='Content-Type: application/json'

curl -s -X POST $BASE/v1/auth/register -H "$JSON" -d '{"email":"ada@example.com","password":"correct-horse"}'
TOKEN=$(curl -s -X POST $BASE/v1/auth/login -H "$JSON" \
  -d '{"email":"ada@example.com","password":"correct-horse"}' | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -s $BASE/v1/me -H "Authorization: Bearer $TOKEN"

curl -s -X POST $BASE/v1/tickets -H "$JSON" -H "Authorization: Bearer $TOKEN" -d '{"title":"VPN down"}'
curl -s "$BASE/v1/tickets?status=open&limit=10" -H "Authorization: Bearer $TOKEN"

AGENT=$(curl -s -X POST $BASE/v1/auth/login -H "$JSON" \
  -d '{"email":"agent@queuedesk.local","password":"agent-demo-password"}' | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -s -X POST $BASE/v1/tickets/1/claim -H "Authorization: Bearer $AGENT"
curl -s -X PATCH $BASE/v1/tickets/1/status -H "$JSON" -H "Authorization: Bearer $AGENT" -d '{"status":"in_progress"}'
curl -s $BASE/v1/tickets/1/events -H "Authorization: Bearer $TOKEN"

curl -s -X DELETE $BASE/v1/auth/session -H "Authorization: Bearer $TOKEN" -w '%{http_code}\n'
```

## Architettura

```
cmd/api        main: config → dipendenze → avvio → shutdown (nient'altro)
cmd/migrate    runner delle migrazioni
cmd/seed       creazione esplicita dell'agent demo
internal/config     Load(getenv) + validazione
internal/auth       password (bcrypt), token (crypto/rand + SHA-256), Service
internal/ticket     modello, regole (visibilità, CanTransition), Service
internal/notify     coda limitata + worker pool
internal/postgres   repository pgx (implementano le interfacce di auth e ticket)
internal/httpapi    handler, middleware, JSON, mappatura errori → HTTP
```

- **Interfacce piccole, dichiarate da chi le usa**: `auth.UserStore` e `auth.SessionStore`,
  `ticket.Store` e `ticket.Publisher`, `httpapi.AuthService`, `httpapi.TicketService` e `httpapi.Pinger`.
  `postgres` dipende da `auth` e `ticket`, mai il contrario.
- **Sessioni**: 32 byte da `crypto/rand` in `base64.RawURLEncoding`; nel DB solo `SHA-256(token)`.
  Un login con email inesistente confronta comunque un hash bcrypt fittizio (tempo costante).
- **Claim**: `UPDATE … WHERE id=$1 AND assignee_id IS NULL RETURNING …` più l'evento di audit, nella
  stessa transazione. Il cambio di stato usa invece `SELECT … FOR UPDATE`.
- **Notifiche**: `TryPublish` non blocca mai (`ErrQueueFull` e contatore `Dropped`); N worker fissi;
  `Shutdown` chiude senza *send on closed channel* e attende entro la deadline. **La coda è in
  memoria: con un crash le notifiche in coda si perdono** (vedi `BACKLOG.md`, outbox).
- **Shutdown**: prima HTTP, poi coda, infine pool PostgreSQL, entro `SHUTDOWN_TIMEOUT`.
- **Log**: JSON con `slog` e `request_id`; mai header, body, password, token o hash.

## Troubleshooting

| Sintomo | Rimedio |
|---|---|
| `invalid configuration: DATABASE_URL: required` | `cp .env.example .env`. Per `go run` fuori dal Makefile: `set -a; . ./.env; set +a`. |
| Porta 55432 o 8080 occupata | Cambia la porta in `compose.yaml` **e** in `.env`/`Makefile` (oppure `HTTP_ADDR` in `.env`). |
| `database "queuedesk_test" does not exist` | Lo script in `db/init/` gira solo alla creazione del volume: `make db-reset`. |
| Test dello schema strani dopo aver cambiato branch | La migrazione 0002 applicata è quella del branch precedente: `make migrate-redo`. |
| `git switch` rifiutato | Hai modifiche non salvate: `git commit -am "..."` oppure `git stash`. |
| Un acceptance test resta appeso | `make test-acceptance` ha `-timeout 2m`: leggi lo stack delle goroutine stampato. |
| 501 `not_implemented` | Atteso: il messaggio indica il TODO da completare. |
| 500 `internal_error` | Guarda il log di `make run`: l'errore completo è lì, con il `request_id`. |
| Login agent 401 | Manca il seed: `make seed` (da rifare dopo `make db-reset`). |
| Pull Docker bloccato | Proxy o rete di Docker Desktop. In alternativa un PostgreSQL locale, aggiornando `DATABASE_URL` e `TEST_DATABASE_URL`. |
| 404/405 in testo semplice su rotte inesistenti | Risposte di default del `ServeMux` (vedi BACKLOG). |
