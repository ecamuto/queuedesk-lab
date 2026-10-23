# Tappa 3 — API ticket

| Giorno | Durata | Branch | TODO |
|---|---|---|---|
| 1 | 60' | `tappa-3-ticket` | TICKET-1 … TICKET-3 |

## Prima di iniziare

```bash
git switch -c lavoro/tappa-3 origin/tappa-3-ticket
make db-up migrate-up   # se il DB era spento; le migrazioni non cambiano
make seed               # se non l'hai già fatto in tappa 2: crea l'agent demo
```

## Obiettivo

Creare e leggere ticket rispettando la policy di visibilità. Il lavoro è tutto nel service: regole
di dominio in Go puro, testate con un fake dello store, senza database. Alla fine due requester
vedono ciascuno solo i propri ticket, un agent li vede tutti, e il ticket di un altro "non esiste".

## Da dove partiamo

Il branch contiene la soluzione delle tappe 1 e 2: login, `/v1/me` e logout funzionano. Le quattro
rotte dei ticket sono già registrate e protette da `requireAuth`, ma il service risponde
`501 not_implemented`:

| Richiesta, oggi | Risposta |
|---|---|
| `POST /v1/tickets` | 501 `TODO(TICKET-1) Create: unsupported operation` |
| `GET /v1/tickets` | 501 `TODO(TICKET-2) List: unsupported operation` |
| `GET /v1/tickets/{id}` | 501 `TODO(TICKET-3) Get: unsupported operation` |
| `GET /v1/tickets/{id}/events` | 501 `TODO(TICKET-3) Events: unsupported operation` |

Cosa trovi già pronto, e perché:

| Già pronto | Perché non lo scrivi tu |
|---|---|
| `internal/ticket/ticket.go` | modello, errori, `ParseStatus`, limiti, **la policy di visibilità nel commento del package** |
| interfaccia `Store` in `service.go` | il contratto verso la persistenza: lo dichiara il service, che lo usa |
| `internal/ticket/fakes_test.go` | `fakeStore`: uno `Store` in memoria che registra le chiamate e l'ultimo filtro |
| `internal/httpapi/ticket_handlers.go` | rotte, DTO, `actor()` (principal → `ticket.Actor`), lettura di `{id}` |
| `respondErr` in `internal/httpapi/errors.go` | `ErrInvalidInput` → 400, `ErrNotFound` → 404, tutto il resto → 500 |
| `internal/postgres/tickets.go` | il repository completo: si **legge** a fine tappa (5'), non si scrive |

| ID | File | Cosa scrivi | Concetti Go |
|---|---|---|---|
| TICKET-1 | `internal/ticket/service.go` | `Create` con validazione | `strings.TrimSpace`, rune contro byte (`utf8.RuneCountInString`), `%w` con un sentinella in testa |
| TICKET-2 | `internal/ticket/service.go` | `List`: filtro, `limit`, scope per ruolo | da stringhe grezze a valori tipizzati, `strconv.Atoi`, struct confrontabili con `==` |
| TICKET-3 | `internal/ticket/service.go` | `Get`, `Events`: policy di visibilità | errori di dominio, `errors.Is` lungo la catena, riuso invece di duplicazione |

Ordine consigliato: TICKET-1 → TICKET-2 → TICKET-3, tutti con unit test sul fake, senza avviare
nulla. Poi evidenze dal vivo con `curl`. I 23' restanti sono lavagna (8' all'inizio), lettura
guidata del repository (5') ed evidenze collettive (10' alla fine).

### Chi fa cosa

```text
POST /v1/tickets {"title": "...", "description": "..."}
  → requireAuth → createTicket (handler: JSON, Actor dal principal)
      → Service.Create (TICKET-1: trim, validazione, ReporterID = actor)
          → Store.CreateTicket (postgres: INSERT ticket + evento "created", una transazione)
  ← 201 Location: /v1/tickets/<id>

GET /v1/tickets/{id}
  → requireAuth → getTicket → Service.Get (TICKET-3: policy)
      → Store.TicketByID
  ← 200 ticket, oppure 404 se non esiste o non è tuo
```

Il service non conosce HTTP né SQL: riceve un `Actor`, restituisce dati o errori di dominio.
Solo `httpapi/errors.go` decide quale status corrisponde a quale errore.

## Metodo di lavoro per ogni TODO

Lo stesso ciclo delle tappe 1 e 2:

1. leggi **dove** viene chiamata la funzione e quale problema risolve;
2. apri `internal/ticket/service_test.go`: nomi, input e risultati attesi sono il contratto;
3. esegui il test mirato prima della modifica e leggi il primo fallimento;
4. implementa un solo passo A/B e rimuovi soltanto lo stub indicato;
5. esegui di nuovo lo stesso test; poi passa al passo successivo;
6. alla fine esegui `make test` e l'intero `make test-acceptance`.

Regole:

- non modificare test, fake, firme delle funzioni, l'interfaccia `Store` o gli ID `TODO(...)`;
- aggiungi gli import quando servono (`strings`, `unicode/utf8`, `strconv`); quando togli l'ultimo
  stub togli anche `"errors"`, che non serve più: Go non accetta import inutilizzati;
- scrivi tutto dentro la funzione del TODO: la soluzione ufficiale non usa funzioni di supporto;
- i messaggi di validazione arrivano al client: usa quelli indicati nei TODO.

## Passo 0 — Ambiente e giro del codice (2')

```bash
make test             # baseline verde
make todo             # TICKET-1 … TICKET-3
make test-acceptance  # 4 test rossi, tutti in internal/ticket/service_test.go
```

| Test rosso | Lo sblocca |
|---|---|
| `TestCreateValidation`, `TestStoreErrorsAreWrapped` | TICKET-1 |
| `TestListFilter` | TICKET-2 |
| `TestGetVisibility` | TICKET-3 (passi A e B) |

Contatore dei test rossi di `make test-acceptance` mentre procedi: 4 → **2** dopo TICKET-1 →
**1** dopo TICKET-2 → **0** dopo TICKET-3.

Leggi il commento del package in `internal/ticket/ticket.go` (la policy) e `fakes_test.go`:
`calls` registra i metodi invocati, `lastF` l'ultimo `ListFilter` ricevuto, `err` simula un guasto.

## Passo 1 — TICKET-1: creazione con validazione (13')

**Dove entra nel servizio.** `createTicket` decodifica il JSON, ricava l'`Actor` dal principal e
chiama `Create`. Un `ErrInvalidInput` diventa 400 con `err.Error()` come messaggio; un ticket
creato diventa 201 con l'header `Location`.

**Perché serve.** La validazione è una regola di dominio: deve valere qualunque sia il canale
(HTTP oggi, una CLI domani). Il CHECK nel database resta l'ultima difesa, ma darebbe un errore SQL
generico (500); il service dà un 400 con un messaggio utile, e senza nemmeno interrogare il DB.

**Come svilupparlo.** Passo A normalizza e valida, passo B salva. Tra i due c'è un confine netto:
con un input non valido lo store non va chiamato.

### Regole di validazione

| Campo | Normalizzazione | Valido | Errore |
|---|---|---|---|
| `title` | `strings.TrimSpace` | da 1 a `MaxTitleLen` (200) **caratteri** | `invalid input: title must be between 1 and 200 characters` |
| `description` | `strings.TrimSpace` | da 0 a `MaxDescriptionLen` (5000) caratteri | `invalid input: description must be at most 5000 characters` |
| `ReporterID` | — | sempre `actor.UserID` | non viene mai dal body |

### Byte contro caratteri

`len("é")` vale 2: `len` conta i **byte** della codifica UTF-8. `utf8.RuneCountInString("é")` vale
1: conta i **caratteri** (rune). Il CHECK SQL usa `char_length`, cioè caratteri, e il test
`title exactly max length` usa 200 volte `é` (400 byte): con `len` viene rifiutato.

### Il sentinella in testa

`fmt.Errorf("%w: title must be ...", ErrInvalidInput)` mette `ErrInvalidInput` **dentro** la catena
e il dettaglio nel testo. `respondErr` lo riconosce con `errors.Is` e risponde 400; un
`errors.New("title must be ...")` avrebbe lo stesso testo ma nessun sentinella: diventerebbe 500.

### Sviluppo e test incrementali

```bash
# Passo A: trim, conteggio in rune, sentinella, store non chiamato.
make test-acceptance RUN=TestCreateValidation
make test-acceptance RUN='TestCreateValidation/title_exactly_max_length'

# Passo B: l'errore dello store arriva avvolto e riconoscibile.
make test-acceptance RUN=TestStoreErrorsAreWrapped
```

Errori frequenti:

- `len(title)` → `title_exactly_max_length` fallisce con `want <nil>`;
- `TrimSpace` dimenticato sul titolo → `valid,_trimmed` riceve il titolo con gli spazi e
  `blank_title` viene accettato;
- store chiamato prima di validare → `store called with invalid input`;
- `errors.New(...)` o `fmt.Errorf` senza `%w: ...` → `Create error = title must be ..., want invalid
  input`;
- `%v` invece di `%w` per l'errore dello store → `does not wrap the store error`;
- `ReporterID` dimenticato → `unexpected ticket {... ReporterID: ...}`;
- `TrimSpace` dimenticato sulla description → *nessun test lo scopre*: rileggi la tabella.

## Passo 2 — TICKET-2: lista con filtro e scope (12')

**Dove entra nel servizio.** `listTickets` passa a `List` le stringhe grezze di `?status=` e
`?limit=`. Il risultato diventa `{"tickets": [...]}`, sempre una lista, anche vuota.

**Perché serve.** La query string è input non fidato. Il service la trasforma in un `ListFilter`
tipizzato e validato, così lo store riceve solo valori già controllati. E decide lui, non il client,
quali ticket può vedere un requester.

**Come svilupparlo.** Passo A costruisce il filtro partendo dai default; passo B interroga lo store
solo se il filtro è valido.

### Contratto di `List`

| Actor | `q.Status` | `q.Limit` | `ListFilter` passato allo store, oppure errore |
|---|---|---|---|
| requester `req-1` | `""` | `""` | `{ReporterID: "req-1", Limit: 20}` |
| agent | `""` | `""` | `{Limit: 20}`: `ReporterID` vuoto = tutti |
| agent | `resolved` | `5` | `{Status: "resolved", Limit: 5}` |
| requester | `""` | `100` | `{ReporterID: "req-1", Limit: 100}` |
| qualsiasi | `closed` | — | `invalid input: status must be one of open, in_progress, resolved` |
| qualsiasi | — | `0`, `101`, `ten` | `invalid input: limit must be an integer between 1 and 100` |

`TestListFilter` confronta il filtro ricevuto dal fake con `!=`: si può fare perché `ListFilter`
contiene solo campi confrontabili (stringhe e un intero). Un campo diverso, anche uno solo, e il
test mostra `filter = {...}, want {...}`.

**Perché `ReporterID` lo imposta il server.** Non esiste un parametro `?reporter_id=`, e se
esistesse verrebbe sovrascritto: per un requester lo scope è una regola, non una preferenza.

### Sviluppo e test incrementali

```bash
# Default e scope per ruolo.
make test-acceptance RUN='TestListFilter/(requester_sees_only_own|agent_sees_all)'

# Validazione di status e limit.
make test-acceptance RUN='TestListFilter/(unknown_status|limit_zero|limit_too_big|limit_not_a_number)'

# Tutto.
make test-acceptance RUN=TestListFilter
```

Errori frequenti:

- `ReporterID` non impostato → `requester_sees_only_own` e `max_limit` falliscono con
  `filter = {ReporterID: ...}, want {ReporterID:req-1 ...}`;
- partire da `ListFilter{}` → `Limit:0` invece di 20;
- solo il controllo `n > MaxListLimit` → `limit_zero` viene accettato;
- `Status(q.Status)` senza `ParseStatus` → `unknown_status` arriva allo store.

## Passo 3 — TICKET-3: visibilità di ticket ed eventi (10')

**Dove entra nel servizio.** `getTicket` chiama `Get`; `ticketEvents` chiama `Events`. Entrambi
ricevono `{id}` già convertito in intero dall'handler (`/v1/tickets/abc` risponde 400 `invalid_id`
prima di arrivare qui).

**Perché serve.** È la regola di sicurezza della tappa: un requester non deve leggere i ticket
altrui, e non deve nemmeno scoprire quali ID esistono.

**Come svilupparlo.** Passo A scrive la policy in `Get`; passo B la riusa in `Events`.

### Policy di visibilità

| Actor | Ticket | `Get` / `Events` | HTTP |
|---|---|---|---|
| agent | qualsiasi esistente | ticket / eventi | 200 |
| requester | aperto da lui | ticket / eventi | 200 |
| requester | aperto da un altro | `ErrNotFound` | **404** |
| qualsiasi | inesistente | `ErrNotFound` (dallo store) | 404 |
| qualsiasi | store guasto | `get ticket <id>: <errore>` | 500 |

**Perché 404 e non 403.** Le righe 3 e 4 producono **lo stesso identico body**. Con un 403 per il
ticket altrui, un requester che prova gli ID 1, 2, 3… scoprirebbe quali esistono e quanti ticket ci
sono. Il package non ha nemmeno un `ErrForbidden` in questa tappa: arriverà con il claim.

**`%w` lungo la catena.** Lo store restituisce `ErrNotFound`; `Get` lo avvolge in
`get ticket 42: ticket not found`; `respondErr` lo ritrova con `errors.Is` → 404. Con `%v` il
testo sarebbe **identico**, ma la catena spezzata: `errors.Is` restituirebbe `false` e il client
riceverebbe 500.

### Sviluppo e test incrementali

```bash
# Passo A da solo: questo caso chiama soltanto Get.
make test-acceptance RUN='TestGetVisibility/missing_ticket'

# Passi A e B: owner, agent, altro requester, per Get e per Events.
make test-acceptance RUN=TestGetVisibility

# Fine tappa.
make test-acceptance
```

Errori frequenti:

- `%v` invece di `%w` in `Get` → `Get error = get ticket 42: ticket not found, want ErrNotFound`:
  il messaggio sembra giusto, il tipo no;
- controllo di visibilità dimenticato → `Get error = <nil>, want ticket not found`;
- `Events` che non passa da `Get` → `Events error = <nil>, want ticket not found`: un requester
  leggerebbe l'audit altrui;
- `errors.New("forbidden")` → non è `ErrNotFound`: test rosso e, dal vivo, 500;
- import `"errors"` lasciato dopo l'ultimo stub → `"errors" imported and not used`.

## Lettura guidata — `postgres/tickets.go` (5')

Non è un TODO. Due cose da guardare:

- **`CreateTicket`**: `Begin` → `defer tx.Rollback(ctx)` → INSERT del ticket → INSERT dell'evento
  `created` → `Commit`. Il `Rollback` dopo un `Commit` riuscito non fa nulla, per questo si può
  differire sempre: qualunque `return` di errore annulla tutto. Ogni query usa `tx`, non `r.pool`.
- **`ListTickets`**: la WHERE cresce con `args = append(args, valore)` e
  `fmt.Sprintf("reporter_id = $%d", len(args))`. Nella stringa SQL entrano solo frammenti scritti da
  noi e segnaposto `$1, $2…`; i valori viaggiano separati, quindi niente SQL injection.

## Evidenze dal vivo

Terminale 1: `make run`. Terminale 2:

```bash
B=http://localhost:8080
J='Content-Type: application/json'
login() { curl -s -X POST $B/v1/auth/login -H "$J" -d "{\"email\":\"$1\",\"password\":\"$2\"}" \
          | sed -E 's/.*"token":"([^"]+)".*/\1/'; }

# Due requester (409 email_taken se esistono già: va bene) e l'agent demo.
curl -s -X POST $B/v1/auth/register -H "$J" -d '{"email":"ada@example.com","password":"correct-horse"}'
curl -s -X POST $B/v1/auth/register -H "$J" -d '{"email":"bob@example.com","password":"correct-horse"}'
ADA=$(login ada@example.com correct-horse)
BOB=$(login bob@example.com correct-horse)
AGENT=$(login agent@queuedesk.local agent-demo-password)

curl -si -X POST $B/v1/tickets -H "$J" -H "Authorization: Bearer $ADA" -d '{"title":"  VPN down  "}'
ID=$(curl -s -X POST $B/v1/tickets -H "$J" -H "Authorization: Bearer $ADA" \
     -d '{"title":"Stampante"}' | sed -E 's/.*"id":([0-9]+).*/\1/')
curl -s -X POST $B/v1/tickets -H "$J" -H "Authorization: Bearer $BOB" -d '{"title":"Mouse rotto"}'

curl -s $B/v1/tickets -H "Authorization: Bearer $BOB"            # solo i ticket di Bob
curl -si $B/v1/tickets/$ID -H "Authorization: Bearer $BOB"       # ticket di Ada visto da Bob
curl -s $B/v1/tickets/999999 -H "Authorization: Bearer $BOB"     # ticket inesistente
curl -s "$B/v1/tickets?status=open" -H "Authorization: Bearer $AGENT"
curl -s $B/v1/tickets/$ID/events -H "Authorization: Bearer $ADA"
```

| Prova | Risultato atteso |
|---|---|
| `POST /v1/tickets` con `"  VPN down  "` | 201, header `Location: /v1/tickets/<id>`, `"title":"VPN down"` (spazi tolti), `"status":"open"`, `"assignee_id":null` |
| titolo vuoto o di soli spazi | 400 `invalid_input`, `title must be between 1 and 200 characters` |
| `?limit=1000`, `?limit=abc` oppure `?status=closed` | 400 `invalid_input` con il messaggio della tabella di TICKET-2 |
| Bob: `GET /v1/tickets` | solo `Mouse rotto` |
| Bob: `GET /v1/tickets/$ID` (di Ada) e `GET /v1/tickets/999999` | **lo stesso identico body** 404 `not_found` |
| Bob: `GET /v1/tickets/$ID/events` | 404 anche per l'audit |
| `GET /v1/tickets/abc` | 400 `invalid_id` (lo decide l'handler, non il service) |
| agent: `GET /v1/tickets?status=open` | i ticket di Ada **e** di Bob |
| Ada: `GET /v1/tickets/$ID/events` | un evento `"kind":"created"` con `"payload":{"title":"Stampante"}` |
| un requester senza ticket: `GET /v1/tickets` | `{"tickets":[]}`, non `null` |

Nel DB (`make db-psql`): `SELECT ticket_id, kind, payload FROM ticket_events ORDER BY id;` mostra un
evento `created` per ogni ticket, scritto nella stessa transazione del ticket.

<details><summary>Aiuto — livello 1</summary>

- Il service valida e applica la policy; il repository parla solo SQL.
- Prima si valida, poi si chiama lo store: un input non valido non deve arrivare al database.
- Il filtro di un requester lo decide il server.
- Un requester non deve neppure sapere che un ticket altrui esiste.
</details>

<details><summary>Aiuto — livello 2</summary>

- `strings.TrimSpace` e `utf8.RuneCountInString` (non `len`: "è" sono 2 byte).
- Errori di validazione: `fmt.Errorf("%w: title must be between 1 and %d characters", ErrInvalidInput, MaxTitleLen)`.
- Errori dello store: `fmt.Errorf("create ticket: %w", err)`.
- `List`: parti da `ListFilter{Limit: DefaultListLimit}`; `ParseStatus` esiste già;
  `strconv.Atoi(q.Limit)` più il controllo `1..MaxListLimit`.
- Requester: `f.ReporterID = actor.UserID`, imposto dal server.
- `Events`: chiama prima `s.Get`, poi `s.store.ListEvents`.
</details>

<details><summary>Aiuto — livello 3 (TICKET-2, passo A)</summary>

```go
f := ListFilter{Limit: DefaultListLimit}
if q.Status != "" {
    st, err := ParseStatus(q.Status)
    if err != nil {
        return nil, err
    }
    f.Status = st
}
if q.Limit != "" {
    n, err := strconv.Atoi(q.Limit)
    if err != nil || n < 1 || n > MaxListLimit {
        return nil, fmt.Errorf("%w: limit must be an integer between 1 and %d", ErrInvalidInput, MaxListLimit)
    }
    f.Limit = n
}
if !actor.IsAgent {
    f.ReporterID = actor.UserID
}
```
</details>

<details><summary>Aiuto — livello 3 (TICKET-3)</summary>

```go
t, err := s.store.TicketByID(ctx, id)
if err != nil {
    return Ticket{}, fmt.Errorf("get ticket %d: %w", id, err)
}
if !actor.IsAgent && t.ReporterID != actor.UserID {
    // ErrNotFound, non "forbidden": un requester non scopre che il ticket esiste
    return Ticket{}, fmt.Errorf("get ticket %d: %w", id, ErrNotFound)
}
return t, nil
```
</details>

## Fine giornata 1

Prima del commit verifica questa checklist:

- **TICKET-1**: titolo e descrizione senza spazi ai bordi; lunghezze contate in caratteri; un
  input non valido dà 400 senza toccare lo store; gli errori dello store restano riconoscibili;
- **TICKET-2**: default 20, limite 1–100, stato validato; un requester vede solo i propri ticket
  qualunque cosa chieda;
- **TICKET-3**: ticket altrui e ticket inesistente danno lo stesso 404, anche per gli eventi;
  l'agent vede tutto;
- `make test`, `make test-race`, `make test-acceptance` e le prove della tabella **Evidenze dal
  vivo** hanno l'esito atteso.

`make test-acceptance` verde, poi `git commit -am "tappa 3"`. Domani si riparte tutti da
`git switch -c lavoro/tappa-4 origin/tappa-4-claim`, che contiene la soluzione delle tappe 1–3.
