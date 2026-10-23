# Tappa 4 — Claim e transazioni

| Giorno | Durata | Branch | TODO |
|---|---|---|---|
| 2 | 60' (dopo 10' di ripresa) | `tappa-4-claim` | CLAIM-1 … CLAIM-4 |

## Prima di iniziare

```bash
git switch -c lavoro/tappa-4 origin/tappa-4-claim
make db-up migrate-up seed                          # se il DB è stato fermato ieri
make seed SEED_AGENT_EMAIL=agent2@queuedesk.local   # un secondo agent, per le evidenze
```

Si riparte tutti da qui: `origin/tappa-4-claim` contiene la soluzione ufficiale delle tappe 1–3,
quindi chi ieri era rimasto indietro riparte allineato.

## Obiettivo

Due agent che fanno claim dello stesso ticket nello stesso istante: **esattamente uno vince**.
Poi le transizioni di stato, riservate all'agent assegnato, e la traduzione dei nuovi errori in
403 e 409.

## Da dove partiamo

Le due rotte nuove sono già registrate e protette da `requireAuth`, ma rispondono
`501 not_implemented`:

| Richiesta, oggi | Risposta |
|---|---|
| `POST /v1/tickets/{id}/claim` | 501 `TODO(CLAIM-2) Claim: unsupported operation` |
| `PATCH /v1/tickets/{id}/status` | 501 `TODO(CLAIM-3) ChangeStatus: unsupported operation` |

Cosa trovi già pronto, e perché:

| Già pronto | Perché non lo scrivi tu |
|---|---|
| errori in `internal/ticket/claim.go` | `ErrForbidden`, `ErrNotAssignee`, `ErrAlreadyClaimed`, `ErrInvalidTransition`: il vocabolario |
| `UpdateStatus` in `internal/postgres/claim.go` | la stessa garanzia con un lock pessimistico (`SELECT … FOR UPDATE`): da leggere e confrontare |
| `internal/httpapi/claim_handlers.go` | handler di claim e cambio di stato |
| `internal/ticket/claim_fakes_test.go` | `ClaimTicket` e `UpdateStatus` del fake, per testare il service senza DB |
| `internal/postgres/claim_test.go` | il test concorrente con la barriera: da leggere con la classe |

| ID | File | Cosa scrivi | Concetti Go |
|---|---|---|---|
| CLAIM-1 | `internal/postgres/claim.go` | `ClaimTicket`: UPDATE condizionale in transazione | `pgx.Tx`, `defer tx.Rollback`, `RETURNING`, `pgx.ErrNoRows`, atomicità delegata al DB |
| CLAIM-2 | `internal/ticket/claim.go` | `Claim`: prima il ruolo, poi lo store | errori sentinella, `%w`, separazione service/store |
| CLAIM-3 | `internal/ticket/claim.go` | `CanTransition`, `ChangeStatus` | `switch` senza `break`, conversione validata (`ParseStatus`) |
| CLAIM-4 | `internal/httpapi/errors.go` | errori del claim → 403/409 | `switch` senza espressione, `errors.Is` contro `==` |

Ordine consigliato: CLAIM-1 (l'unico che richiede PostgreSQL) → CLAIM-2 → CLAIM-3 → CLAIM-4. I
16' restanti sono lavagna (10' all'inizio) ed evidenze collettive (6' alla fine).

### Il percorso di un claim

```text
POST /v1/tickets/{id}/claim
  → requireAuth → claimTicket (handler)
      → Service.Claim (CLAIM-2: requester? 403 senza toccare il DB)
          → TicketRepo.ClaimTicket (CLAIM-1: BEGIN; UPDATE ... WHERE assignee_id IS NULL;
                                    evento "claimed"; COMMIT)
  ← 200 ticket assegnato, oppure un errore che respondErr (CLAIM-4) traduce in 403/404/409
```

La concorrenza la risolve PostgreSQL, non Go: nessun mutex. Le richieste concorrenti possono
arrivare anche da più istanze del servizio, e un `sync.Mutex` proteggerebbe un processo solo.

## Metodo di lavoro per ogni TODO

Lo stesso ciclo delle tappe precedenti:

1. leggi **dove** viene chiamata la funzione e quale problema risolve;
2. apri il relativo `_test.go`: nomi, input e risultati attesi sono il contratto;
3. esegui il test mirato prima della modifica e leggi il primo fallimento;
4. implementa un solo passo e rimuovi soltanto lo stub indicato;
5. esegui di nuovo lo stesso test; poi passa al passo successivo;
6. alla fine esegui `make test` e l'intero `make test-acceptance`.

Regole:

- non modificare test, fake, firme, l'interfaccia `Store` o gli ID `TODO(...)`;
- in `ClaimTicket` ogni query usa `tx`, mai `r.pool`;
- un test concorrente verde una volta non dimostra nulla: ripetilo (comando in CLAIM-1);
- gli import in questi file servono già ad altro codice: non devi aggiungerne né toglierne.

## Passo 0 — Ambiente e giro del codice (2')

```bash
make test             # baseline verde
make todo             # CLAIM-1 … CLAIM-4
make test-acceptance  # 7 test rossi: quelli della tabella sotto
```

| File di test | Test rossi | Li sblocca |
|---|---|---|
| `internal/postgres/claim_test.go` | `TestClaimConcurrentSingleWinner`, `TestClaimErrors` | CLAIM-1 |
| `internal/postgres/claim_test.go` | `TestUpdateStatus` | CLAIM-1 (prepara i dati) e CLAIM-3 (`CanTransition`) |
| `internal/ticket/claim_test.go` | `TestClaim` | CLAIM-2 |
| `internal/ticket/claim_test.go` | `TestCanTransition`, `TestChangeStatus` | CLAIM-3 |
| `internal/httpapi/claim_handlers_test.go` | `TestClaimErrorMapping` | CLAIM-4 |

Contatore dei test rossi di `make test-acceptance` mentre procedi: 7 → **5** dopo CLAIM-1 →
**4** dopo CLAIM-2 → **1** dopo CLAIM-3 → **0** dopo CLAIM-4.

## Passo 1 — CLAIM-1: l'UPDATE atomico (20')

**Dove entra nel servizio.** `Service.Claim` (CLAIM-2) chiama `ClaimTicket` dopo aver controllato
il ruolo. È l'unico punto del sistema che assegna un ticket.

**Perché serve.** È il cuore del laboratorio. Due agent cliccano "prendo io" nello stesso istante:
uno solo deve vincere, l'altro deve ricevere 409, e nell'audit deve comparire **un solo** evento
`claimed`.

**Come svilupparlo.** Prima disegna la race, poi scrivi quattro passi: transazione, UPDATE
condizionale, distinzione delle due cause di "zero righe", evento e commit.

### La race di "controlla e poi aggiorna"

```text
A: SELECT assignee_id → NULL
B: SELECT assignee_id → NULL
A: UPDATE ... SET assignee_id = A   ✔
B: UPDATE ... SET assignee_id = B   ✔  ← sovrascrive A: due vincitori, due eventi
```

Con la condizione **dentro** l'UPDATE (`WHERE id = $1 AND assignee_id IS NULL`), "controlla e
scrivi" diventa un'operazione sola. Chi arriva secondo si blocca sul lock della riga; quando il
primo fa commit, PostgreSQL (READ COMMITTED) **rivaluta la WHERE** sulla versione nuova della riga,
trova `assignee_id` valorizzato e aggiorna zero righe.

### Contratto di `ClaimTicket`

| Situazione | UPDATE | Dopo | Restituisce | HTTP |
|---|---|---|---|---|
| ticket libero, arrivi primo | 1 riga (`RETURNING`) | evento `claimed`, `Commit` | ticket assegnato | 200 |
| ticket già assegnato (anche a te) | 0 righe → `pgx.ErrNoRows` | `SELECT EXISTS` → `true` | `ticket.ErrAlreadyClaimed` | 409 |
| ticket inesistente | 0 righe → `pgx.ErrNoRows` | `SELECT EXISTS` → `false` | `ticket.ErrNotFound` | 404 |
| errore del DB | — | `Rollback` (dal `defer`) | `claim ticket: ...: <errore>` | 500 |

Il `SELECT EXISTS` si fa **solo dopo** un UPDATE fallito e nella stessa transazione. Prima
dell'UPDATE non serve nulla: leggere `assignee_id` e decidere in Go riaprirebbe la race, e un
`EXISTS` preliminare sarebbe solo una query in più anche per chi vince.

### Il test concorrente

`assertSingleWinner` in `internal/postgres/claim_test.go`, da leggere:

- ogni goroutine chiama `ready.Done()` e poi si blocca su `<-start`;
- il test aspetta `ready.Wait()` (tutte pronte), poi `close(start)`: un canale chiuso sblocca
  **tutte** le goroutine in attesa nello stesso istante;
- verifica: esattamente un vincitore, gli altri `ErrAlreadyClaimed`, un solo evento `claimed`,
  assegnatario = vincitore; con 2 e 8 agent, per 10 round.

Niente `time.Sleep`: rallenterebbe il test e comunque non garantirebbe la sovrapposizione.

### Sviluppo e test incrementali

```bash
# Passi A–D: richiede PostgreSQL (make db-up).
make test-acceptance RUN=TestClaimErrors
make test-acceptance RUN=TestClaimConcurrentSingleWinner

# Ripeti il test concorrente 20 volte: un risultato verde singolo non basta.
TEST_DATABASE_URL='postgres://queuedesk:queuedesk@localhost:55432/queuedesk_test?sslmode=disable' \
  go test -tags acceptance -race -count=20 -run TestClaimConcurrentSingleWinner ./internal/postgres
```

Errori frequenti:

- `SELECT assignee_id` e poi `UPDATE` → `N winners [...], want exactly 1`: nelle nostre prove è
  rosso 20 volte su 20, ma una race può anche nascondersi, per questo si ripete con `-count=20`;
- `pgx.ErrNoRows` non gestito → `unexpected error claim ticket: update: no rows in result set`;
- nessuna distinzione fra le due cause → con sempre `ErrAlreadyClaimed` il ticket inesistente dà
  409 (`TestClaimErrors`); con sempre `ErrNotFound` i perdenti ricevono `unexpected error ticket
  not found`;
- `tx.Commit` dimenticato → `8 winners [...]`: ogni transazione viene annullata dal `defer`, chi
  aspettava il lock ritrova `assignee_id` NULL e "vince" anche lui, ma nel DB non resta nulla;
- evento scritto con `r.pool` invece di `tx` → *nessun test lo scopre*: un crash fra le due query
  lascerebbe un claim senza evento.

**Confronto con `UpdateStatus`.** Subito sotto, `UpdateStatus` risolve lo stesso problema con un
lock pessimistico: `SELECT … FOR UPDATE` blocca la riga, il codice Go verifica assegnatario e
transizione, poi aggiorna. Due query e un lock più lungo, ma regole arbitrarie in Go. Conviene
quando la decisione non si esprime in una WHERE.

## Passo 2 — CLAIM-2: il claim nel service (6')

**Dove entra nel servizio.** L'handler `claimTicket` passa a `Claim` l'`Actor` ricavato dal
principal e l'`{id}` del path.

**Perché serve.** Il ruolo è una regola di dominio: la verifica il service, prima di qualsiasi
query. L'atomicità invece è un problema di dati: la garantisce lo store.

**Come svilupparlo.** Quattro righe: controllo del ruolo, chiamata allo store, `%w`, return.

| Actor | Ticket | Store chiamato? | `Claim` restituisce |
|---|---|---|---|
| requester | qualsiasi | **no** | `ErrForbidden` |
| agent | libero | sì | ticket con `AssigneeID` = agent |
| agent | già assegnato | sì | `claim ticket <id>: ticket already claimed` |
| agent | inesistente | sì | `claim ticket <id>: ticket not found` |

Non aggiungere un controllo in Go come `TicketByID` seguito da `if t.AssigneeID != nil`:
reintrodurrebbe la race che CLAIM-1 elimina.

```bash
make test-acceptance RUN='^TestClaim$'
```

Errori frequenti:

- ruolo controllato dopo lo store → `store called = true, want false`;
- controllo "già assegnato" in Go con un errore inventato → `Claim error = ticket 1 already
  assigned, want ticket already claimed`;
- `%v` invece di `%w` → `Claim error = claim ticket 1: ticket already claimed, want ticket already
  claimed`: il testo contiene l'errore giusto, ma la catena è spezzata.

## Passo 3 — CLAIM-3: transizioni di stato (10')

**Dove entra nel servizio.** `PATCH /v1/tickets/{id}/status` → `changeStatus` → `ChangeStatus`
(passo B) → `UpdateStatus` nel repository, che sotto lock chiama `CanTransition` (passo A).

**Perché serve.** Il ciclo di vita del ticket è una regola di dominio: deve stare in un posto solo,
testabile senza database, e applicata dove lo stato non può cambiare sotto i piedi.

**Come svilupparlo.** Passo A è uno `switch`; passo B ripete lo schema di `Claim` con una
conversione validata in mezzo.

### Transizioni ammesse

```text
open ──▶ in_progress ──▶ resolved
  ▲          │  ▲            │
  └──────────┘  └────────────┘
```

| Da \ A | `open` | `in_progress` | `resolved` |
|---|---|---|---|
| `open` | no | **sì** | no |
| `in_progress` | **sì** | no | **sì** |
| `resolved` | no | **sì** | no |

Restare nello stesso stato non è una transizione; uno stato sconosciuto non transita mai. Il claim
assegna ma non cambia lo stato, e nessuna transizione tocca l'assegnatario: un ticket rimesso in
`open` resta dell'agent che ne ha fatto claim.

### Chi controlla cosa in `ChangeStatus`

| Controllo | Dove | Errore | HTTP |
|---|---|---|---|
| ruolo agent | service, prima di tutto | `ErrForbidden` | 403 |
| stato valido (`"done"`?) | service, con `ParseStatus` | `ErrInvalidInput` | 400 |
| sei l'assegnatario? | store, dopo `FOR UPDATE` | `ErrNotAssignee` | 403 |
| transizione ammessa? | store, dopo `FOR UPDATE`, con `CanTransition` | `ErrInvalidTransition` | 409 |

Assegnatario e transizione non si controllano nel service: fra il controllo e l'UPDATE lo stato
potrebbe cambiare. Sotto `FOR UPDATE` non può.

### Sviluppo e test incrementali

```bash
# Passo A: le 9 coppie di stati più uno stato sconosciuto.
make test-acceptance RUN=TestCanTransition

# Passo B: ruolo, ParseStatus, store (il fake usa CanTransition).
make test-acceptance RUN=TestChangeStatus

# Con PostgreSQL: claim, transizioni ed eventi in sequenza.
make test-acceptance RUN=TestUpdateStatus
```

Errori frequenti:

- `return true` come default dello `switch` → `TestCanTransition` segnala tutte le coppie sbagliate;
- `in_progress → open` dimenticata → `CanTransition(in_progress, open) = false, want true`;
- `Status(status)` invece di `ParseStatus` → `invalid_status_value`: `"done"` arriva allo store e
  diventa `status transition not allowed` (409) invece di `invalid input` (400).

## Passo 4 — CLAIM-4: errori → status HTTP (6')

**Dove entra nel servizio.** `respondErr` è l'unico punto che traduce errori di dominio in status
HTTP. Oggi i quattro errori nuovi finiscono nel `default`: 500 `internal_error` e una riga ERROR
nel log. Lo vedi dal vivo se completi CLAIM-1…3 prima di CLAIM-4: chi perde il claim riceve 500.

**Perché serve.** 403 dice "sei autenticato, ma non puoi farlo"; 409 dice "la richiesta è valida,
ma lo stato attuale del ticket la impedisce". Un client li gestisce in modo diverso.

**Come svilupparlo.** Quattro `case errors.Is(...)` nello `switch` senza espressione.

| Errore | Status | Codice | Messaggio |
|---|---|---|---|
| `ticket.ErrForbidden` | 403 | `forbidden` | `this operation requires the agent role` |
| `ticket.ErrNotAssignee` | 403 | `not_assignee` | `only the assigned agent can change the status` |
| `ticket.ErrAlreadyClaimed` | 409 | `already_claimed` | `ticket already claimed by another agent` |
| `ticket.ErrInvalidTransition` | 409 | `invalid_transition` | `err.Error()`: dice da quale a quale stato |

**`errors.Is` e non `==`.** L'errore arriva avvolto da store e service: `claim ticket 7: ticket
already claimed`. `==` confronta solo lo strato esterno e fallisce; `errors.Is` risale la catena
creata da `%w`.

```bash
make test-acceptance RUN=TestClaimErrorMapping
make test-acceptance            # fine tappa: 0 rossi
```

Errori frequenti:

- `err == ticket.ErrAlreadyClaimed` → `claim_conflict`: `status = 500, want 409`;
- status sbagliato → `status = 400, want 409`;
- messaggio fisso invece di `err.Error()` per `invalid_transition` → *nessun test lo scopre*: il
  test controlla solo status e codice, ma il client perde l'informazione "da quale a quale stato".

## Evidenze dal vivo

Terminale 1: `make run`. Terminale 2 (i due agent li hai creati in "Prima di iniziare"):

```bash
B=http://localhost:8080
J='Content-Type: application/json'
login() { curl -s -X POST $B/v1/auth/login -H "$J" -d "{\"email\":\"$1\",\"password\":\"$2\"}" \
          | sed -E 's/.*"token":"([^"]+)".*/\1/'; }

curl -s -o /dev/null -X POST $B/v1/auth/register -H "$J" \
     -d '{"email":"ada@example.com","password":"correct-horse"}'          # 409 se esiste già: va bene
ADA=$(login ada@example.com correct-horse)
AGENT=$(login agent@queuedesk.local agent-demo-password)
AGENT2=$(login agent2@queuedesk.local agent-demo-password)
ID=$(curl -s -X POST $B/v1/tickets -H "$J" -H "Authorization: Bearer $ADA" \
     -d '{"title":"Claim conteso"}' | sed -E 's/.*"id":([0-9]+).*/\1/')

# Due agent, due claim nello stesso istante: 200 e 409, in qualsiasi ordine.
for T in $AGENT $AGENT2; do
  curl -s -o /dev/null -w '%{http_code}\n' -X POST $B/v1/tickets/$ID/claim -H "Authorization: Bearer $T" &
done; wait

curl -s -X POST $B/v1/tickets/$ID/claim -H "Authorization: Bearer $ADA"            # requester
for T in $AGENT $AGENT2; do                                                          # chi ha vinto?
  curl -s -o /dev/null -w '%{http_code} ' -X PATCH $B/v1/tickets/$ID/status -H "$J" \
       -H "Authorization: Bearer $T" -d '{"status":"resolved"}'
done; echo

for S in in_progress resolved; do                                                    # solo il vincitore
  for T in $AGENT $AGENT2; do
    curl -s -o /dev/null -w '%{http_code} ' -X PATCH $B/v1/tickets/$ID/status -H "$J" \
         -H "Authorization: Bearer $T" -d "{\"status\":\"$S\"}"
  done; echo
done
curl -s $B/v1/tickets/$ID/events -H "Authorization: Bearer $ADA" | grep -o '"kind":"[a-z_]*"'
```

| Prova | Risultato atteso |
|---|---|
| due claim simultanei di due agent | `200` e `409`, in qualsiasi ordine |
| claim con il token di Ada (requester) | 403 `forbidden` |
| `resolved` su un ticket `open` | `409` dall'agent assegnato (`invalid_transition`), `403` dall'altro (`not_assignee`) |
| `in_progress`, poi `resolved` | `200` per il vincitore, `403` per l'altro, due volte |
| `GET …/events` | `created`, `claimed`, `status_changed`, `status_changed`: **un solo** `claimed` |
| log di `make run` | nessuna riga ERROR: 403 e 409 non sono errori del server |

Ripeti il blocco più volte: il vincitore cambia, la forma del risultato no.

<details><summary>Aiuto — livello 1</summary>

Disegna su una linea del tempo due agent che eseguono "leggi assignee; se è vuoto, aggiorna".
Dove si rompe? Cosa deve fare il database perché non vincano entrambi?
</details>

<details><summary>Aiuto — livello 2</summary>

- Metti la condizione dentro l'UPDATE stesso: `WHERE id = $1 AND assignee_id IS NULL`. Con
  `RETURNING` sai subito se hai vinto (una riga) o perso (`pgx.ErrNoRows`).
- Zero righe ha due cause: le distingui con `SELECT EXISTS (...)` **nella stessa transazione**.
- Service: prima il ruolo, poi lo store; gli errori dello store avvolti con `%w`.
- `CanTransition`: uno `switch from` con un `return` per ogni stato.
- CLAIM-4: `errors.Is` segue la catena di `%w`; un confronto con `==` fallirebbe sull'errore
  avvolto.
</details>

<details><summary>Aiuto — livello 3 (CLAIM-1)</summary>

```go
t, err := scanTicket(tx.QueryRow(ctx, `UPDATE tickets SET assignee_id = $2, updated_at = now()
    WHERE id = $1 AND assignee_id IS NULL RETURNING `+ticketColumns, id, agentID))
if errors.Is(err, pgx.ErrNoRows) {
    var exists bool
    // tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tickets WHERE id = $1)`, id).Scan(&exists)
    // !exists → ticket.ErrNotFound ; exists → ticket.ErrAlreadyClaimed
}
// altro err → "claim ticket: update: %w"; poi insertEvent(..., ticket.EventClaimed, ...) → tx.Commit(ctx)
```
</details>

## Fine tappa

Prima del commit verifica questa checklist:

- **CLAIM-1**: un solo UPDATE condizionale, nessun SELECT preliminare; 404 e 409 distinti dopo,
  nella stessa transazione; evento `claimed` solo per il vincitore; il test concorrente resta
  verde con `-count=20`;
- **CLAIM-2**: un requester riceve 403 senza query; gli errori dello store restano riconoscibili;
- **CLAIM-3**: solo le quattro transizioni ammesse; uno stato non valido dà 400, non 409;
- **CLAIM-4**: 403 e 409 con `errors.Is`; `invalid_transition` dice da quale a quale stato;
- `make test`, `make test-race`, `make test-acceptance` e le prove della tabella **Evidenze dal
  vivo** hanno l'esito atteso.

`make test-acceptance` verde, poi `git commit -am "tappa 4"` e passa alla tappa 5 con
`git switch -c lavoro/tappa-5 origin/tappa-5-notify`.
