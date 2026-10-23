# Tappa 5 — Worker pool e shutdown

| Giorno | Durata | Branch | TODO |
|---|---|---|---|
| 2 | 50' | `tappa-5-notify` | NOTIFY-1 … NOTIFY-5 |

## Prima di iniziare

```bash
git switch -c lavoro/tappa-5 origin/tappa-5-notify
```

## Obiettivo

Prima si costruisce un worker pool **isolato**, senza HTTP né database. Poi lo si **integra** nel
servizio: le notifiche partono dopo il commit, chi le pubblica non aspetta mai, e allo shutdown la
coda viene svuotata nell'ordine giusto ed entro una deadline.

## Da dove partiamo

Il servizio funziona già per intero, ma senza notifiche: `main` passa `nil` come publisher a
`ticket.NewService`. Il package `internal/notify` è nuovo, compila, ma `TryPublish` restituisce
`TODO(NOTIFY-1)` e `Start`/`Shutdown` non fanno nulla.

Cosa trovi già pronto, e perché:

| Già pronto | Perché non lo scrivi tu |
|---|---|
| `Queue`, `New`, `deliver`, `Stats`, `LogHandler` in `queue.go` | la struttura dati (channel, `RWMutex`, flag `closed`, `WaitGroup`, `cancel`, contatori atomici) e le parti senza concorrenza |
| `Publisher` e `publish` in `internal/ticket/service.go` | l'interfaccia piccola che `*notify.Queue` soddisfa, e la costruzione della `Notification`; con `pub == nil` nessuna notifica |
| `internal/notify/queue_test.go` | i test, anche con `testing/synctest`: orologio virtuale, deadline istantanee e deterministiche |
| `internal/ticket/notify_test.go` | un publisher finto che registra le notifiche ricevute |

| ID | File | Cosa scrivi | Concetti Go |
|---|---|---|---|
| NOTIFY-1 | `internal/notify/queue.go` | `TryPublish` non bloccante e sicuro rispetto alla chiusura | `select` con `default`, `sync.RWMutex`, `atomic.Uint64` |
| NOTIFY-2 | `internal/notify/queue.go` | `Start` e ciclo del worker | worker pool fisso, `context.WithCancel`, `sync.WaitGroup.Go`, `n, ok := <-ch` |
| NOTIFY-3 | `internal/notify/queue.go` | `Shutdown` con deadline | chi chiude il channel, WaitGroup con timeout (goroutine + `done`), `select` sulla deadline |
| NOTIFY-4 | `internal/ticket/service.go`, `claim.go` | `s.publish(...)` dopo create, claim e cambio di stato | effetti collaterali solo dopo il successo, best effort |
| NOTIFY-5 | `cmd/api/main.go` | creare, avviare e passare la coda; chiuderla nello shutdown | ordine dello shutdown, un'unica deadline, `errors.Join` |

Due parti: **A** (NOTIFY-1…3, circa 22') si sviluppa solo contro i test del package `notify`;
**B** (NOTIFY-4…5, circa 12') integra la coda nel servizio. I 16' restanti sono lavagna (8'
all'inizio) ed evidenze collettive (8' alla fine).

### Il percorso di una notifica

```text
POST /v1/tickets → Service.Create → store (COMMIT) → s.publish (NOTIFY-4)
                                                       → queue.TryPublish (NOTIFY-1)
                                                            │ non blocca mai: piena → scarta e conta
                                                            ▼
                               ┌─ worker 1 ─┐
              q.ch (buffer) ──▶├─ worker 2 ─┤──▶ LogHandler: 200 ms, poi "notification sent"
                               └─ worker N ─┘     (NOTIFY-2: N goroutine fisse)

Ctrl+C → srv.Shutdown (HTTP) → queue.Shutdown (NOTIFY-3: chiude, svuota, deadline) → pool.Close
```

## Metodo di lavoro per ogni TODO

Lo stesso ciclo delle tappe precedenti, con una differenza importante nella parte A:

1. leggi **dove** viene chiamata la funzione e quale problema risolve;
2. apri il relativo `_test.go`: nomi, input e risultati attesi sono il contratto;
3. esegui il test mirato prima della modifica e leggi il primo fallimento;
4. implementa un passo e rimuovi soltanto lo stub indicato;
5. esegui di nuovo lo stesso test; poi passa al passo successivo;
6. alla fine esegui `make test` e l'intero `make test-acceptance`.

Regole:

- non modificare test, firme, campi di `Queue` o gli ID `TODO(...)`;
- lancia sempre i test di `notify` con `-race`: un test concorrente senza race detector è a metà;
- **fra NOTIFY-1 e NOTIFY-3 non lanciare l'intero package `notify`**. I tre TODO dipendono l'uno
  dall'altro, e uno stato intermedio non fallisce in modo pulito:

| Hai completato | `go test -tags acceptance -race ./internal/notify` | Perché |
|---|---|---|
| solo NOTIFY-1 | `TestShutdownDrainsPendingNotifications` resta bloccato fino al timeout | nessun worker consuma la coda |
| NOTIFY-1 e 2, non il 3 | `panic: send on closed channel` | `Shutdown` non aspetta i worker: il test chiude il **suo** channel mentre loro ci scrivono ancora |
| NOTIFY-1 e 3, non il 2 | `TestShutdownDrainsPendingNotifications` resta bloccato | come sopra: nessun worker |
| NOTIFY-1, 2 e 3 | verde | — |

Il secondo caso è istruttivo: è lo stesso panic che `TryPublish` deve evitare, provocato da chi
chiude un channel mentre qualcun altro ci scrive.

## Passo 0 — Ambiente e giro del codice (2')

```bash
make test             # baseline verde
make todo             # NOTIFY-1 … NOTIFY-5
make test-acceptance  # 7 test rossi: quelli della tabella sotto
```

| File di test | Test rossi | Li sblocca |
|---|---|---|
| `internal/notify/queue_test.go` | `TestQueueDeliversEveryNotification`, `TestTryPublishWhenFullDropsWithoutBlocking`, `TestTryPublishAfterShutdownReturnsErrClosed`, `TestConcurrentPublishDuringShutdown`, `TestShutdownDrainsPendingNotifications`, `TestShutdownDeadlineCancelsWorkers` | NOTIFY-1 … NOTIFY-3 |
| `internal/ticket/notify_test.go` | `TestPublishesAfterSuccess` | NOTIFY-4 |

Verdi già con lo stub, e devono restarlo: `TestWorkersStopWhenStartContextIsCancelled`,
`TestNoPublishOnFailure`, `TestOperationsSucceedWhenQueueIsFull` (oltre a `TestNewValidatesArguments`
e `TestLogHandlerRespectsCancellation`, che provano codice già scritto). NOTIFY-5 non ha test
automatici: si verifica dal vivo.

Contatore dei test rossi di `make test-acceptance`: 7 → **1** dopo NOTIFY-1…3 → **0** dopo
NOTIFY-4.

Leggi la struttura `Queue` in `queue.go`, in particolare il commento su `mu`: è il contratto fra
`TryPublish` e `Shutdown`.

## Parte A — il package `notify` da solo

`internal/notify` non dipende da nulla del progetto. Si lavora solo con:

```bash
go test -tags acceptance -race ./internal/notify     # non serve il database
```

### Passo 1 — NOTIFY-1: invio non bloccante (6')

**Dove entra nel servizio.** `ticket.Service.publish` chiama `TryPublish` dopo ogni operazione
riuscita. Chi chiama ha già fatto commit e sta rispondendo a una richiesta HTTP.

**Perché serve.** Le notifiche sono *best effort*: se la coda è piena si scarta e si conta, ma la
richiesta HTTP non deve mai aspettare né fallire per questo.

**Come svilupparlo.** Read lock, controllo di `closed`, invio con `select` e `default`.

| Stato della coda | `TryPublish` restituisce | Contatore |
|---|---|---|
| c'è posto nel buffer | `nil` | `published` + 1 |
| buffer pieno | `ErrQueueFull`, **subito** | `dropped` + 1 |
| `Shutdown` già chiamato | `ErrClosed` | nessuno |

**Perché un `RWMutex`.** Più produttori possono inviare insieme (read lock), ma nessuno mentre
`Shutdown` chiude il channel (write lock). Senza lock, un invio può arrivare su un channel appena
chiuso: `panic: send on closed channel`.

```bash
# Il solo test del package che regge da solo: non avvia worker.
make test-acceptance RUN=TestTryPublishWhenFullDropsWithoutBlocking
```

Errori frequenti:

- `q.ch <- n` senza `select`/`default` → `TestTryPublishWhenFullDropsWithoutBlocking` resta
  bloccato fino al timeout: a coda piena l'invio aspetta per sempre;
- controllo di `q.closed` dimenticato → `panic: send on closed channel` (visibile dopo NOTIFY-3);
- lock dimenticato → `WARNING: DATA RACE` in `TestConcurrentPublishDuringShutdown` (visibile dopo
  NOTIFY-3, con `-race`).

### Passo 2 — NOTIFY-2: worker pool (6')

**Dove entra nel servizio.** `main` chiama `Start` una sola volta all'avvio (NOTIFY-5).

**Perché serve.** Un numero fisso di goroutine consuma la coda, qualunque sia il traffico: niente
goroutine per evento.

**Come svilupparlo.** Passo A in `Start` (context cancellabile e N goroutine), passo B in `work`
(il ciclo con `select`).

| Il worker vede | Fa |
|---|---|
| una notifica (`n, ok := <-q.ch` con `ok == true`) | `q.deliver(ctx, id, n)`, poi torna ad aspettare |
| coda chiusa **e vuota** (`ok == false`) | `return` |
| `ctx.Done()` (deadline di `Shutdown`, o ctx di `Start` cancellato) | `return`, anche se la coda non è vuota |

Dopo `close(q.ch)` il channel restituisce ancora tutto ciò che è nel buffer, e solo alla fine
`ok == false`: per questo chiudere significa "svuota e poi esci", non "fermati".

Si verifica insieme a NOTIFY-3: senza `Shutdown` nessun test può aspettare i worker.

Errori frequenti:

- `ctx, q.cancel := context.WithCancel(ctx)` → non compila: `non-name q.cancel on left side of
  :=`. Un campo si assegna con `=`;
- `for n := range q.ch` senza `select` su `ctx.Done()` → *nessun test lo scopre*: il worker non si
  accorge della cancellazione, e se la coda non viene mai chiusa resta vivo per sempre;
- `wg.Add` fatto dentro la goroutine (se non usi `wg.Go`) → race fra `Add` e `Wait`, a volte
  segnalata da `-race`.

### Passo 3 — NOTIFY-3: shutdown con deadline (10')

**Dove entra nel servizio.** `main` chiama `Shutdown` dopo lo shutdown HTTP, con la stessa
deadline; appena ritorna, chiude il pool PostgreSQL.

**Perché serve.** Allo spegnimento le notifiche già accettate vanno consegnate, ma non all'infinito:
oltre la deadline si interrompe, si conta e si esce.

**Come svilupparlo.** A: chiudere la coda una volta sola, sotto lock. B: aspettare il WaitGroup in
una goroutine che chiude `done`. C: `select` fra `done` e `ctx.Done()`.

| Situazione | `Shutdown` | Dopo |
|---|---|---|
| prima chiamata | `closed = true`, `close(q.ch)` | i worker svuotano il buffer ed escono |
| seconda chiamata | non chiude di nuovo (il flag) | `close` di un channel chiuso sarebbe `panic` |
| worker finiti prima della deadline | `return nil` | — |
| deadline scaduta | `q.cancel()`, poi `<-done`, poi errore con `len(q.ch)` e `ctx.Err()` | le notifiche rimaste sono perse |

**Perché `<-done` anche dopo la deadline.** Quando `Shutdown` ritorna, `main` chiude il pool del
database. Un worker ancora dentro un handler lavorerebbe su un pool chiuso. L'attesa è breve perché
il contratto di `Handler` impone di rispettare `ctx`. Lo verifica `TestShutdownDeadlineCancelsWorkers`:
il suo handler impiega 100 ms (dell'orologio finto) a fermarsi dopo la cancellazione, e il test
controlla che il fallimento sia già contato quando `Shutdown` ritorna.

```bash
go test -tags acceptance -race ./internal/notify     # parte A completa: tutto verde
```

Errori frequenti:

- flag `closed` dimenticato → `panic: close of closed channel` in
  `TestTryPublishAfterShutdownReturnsErrClosed`, che chiama `Shutdown` due volte;
- `close(q.ch)` senza lock → `WARNING: DATA RACE` in `TestConcurrentPublishDuringShutdown`;
- `q.wg.Wait()` chiamato direttamente invece che in una goroutine → `panic: deadlock: all goroutines
  in bubble are blocked` in `TestShutdownDeadlineCancelsWorkers`: la deadline non può più scattare;
- `q.cancel()` chiamato subito invece che alla deadline → `TestQueueDeliversEveryNotification`
  fallisce: le notifiche in coda non vengono consegnate;
- `<-done` dimenticato dopo `q.cancel()` → `Failed = 0 right after Shutdown, want 1 (Shutdown must
  wait for the workers)` in `TestShutdownDeadlineCancelsWorkers`: vedi sopra.

## Parte B — integrazione

### Passo 4 — NOTIFY-4: pubblicare dopo il successo (4')

**Dove entra nel servizio.** In `Create`, `Claim` e `ChangeStatus`, dopo il controllo dell'errore
dello store e prima del `return`.

**Perché serve.** Lo store ha già fatto commit: notificare prima, o anche in caso di errore,
annuncerebbe qualcosa che non è successo. Il rovescio della medaglia: un crash fra commit e
`publish` perde la notifica (per garanzie servirebbe un'outbox: vedi `BACKLOG.md`).

| Metodo | Chiamata |
|---|---|
| `Create` | `s.publish(ctx, EventCreated, t, actor.UserID)` |
| `Claim` | `s.publish(ctx, EventClaimed, t, actor.UserID)` |
| `ChangeStatus` | `s.publish(ctx, EventStatusChanged, t, actor.UserID)` |

`publish` non restituisce errori: coda piena o chiusa diventano un WARN nel log, e l'operazione
resta riuscita.

```bash
make test-acceptance RUN='TestPublishesAfterSuccess|TestNoPublishOnFailure|TestOperationsSucceedWhenQueueIsFull'
make test-acceptance     # 0 rossi
```

Errori frequenti:

- `publish` prima del controllo dell'errore → `TestNoPublishOnFailure`: `notifications published
  for failed operations`;
- `publish` dimenticato in un metodo, o con il `EventKind` sbagliato →
  `TestPublishesAfterSuccess`: `notifications = [...], want [created claimed status_changed]`.

### Passo 5 — NOTIFY-5: la coda in `main` (8')

**Dove entra nel servizio.** `run` compone il servizio; `shutdown` lo spegne.

**Perché serve.** È qui che si decide l'ordine di vita dei componenti: la coda deve esistere prima
che arrivino richieste e deve chiudersi dopo l'ultima.

**Come svilupparlo.** Passo A: `notify.New`, `queue.Start(context.Background())`, `queue` al posto
di `nil`. Passo B: `shutdown` riceve la coda e la chiude dopo l'HTTP.

| Ordine dello shutdown | Perché |
|---|---|
| 1. `srv.Shutdown(ctx)` | niente nuove richieste; quelle in corso possono ancora pubblicare |
| 2. `queue.Shutdown(ctx)`, **stesso** `ctx` | la coda si chiude solo quando nessuno può più scriverci; un'unica deadline per tutto |
| 3. `pool.Close()` (il `defer` in `run`) | i worker hanno finito: nessuno usa più il database |

**Perché `context.Background()` in `Start`.** Il `ctx` di `run` viene cancellato al primo Ctrl+C:
con quello i worker si fermerebbero subito, invece di svuotare la coda durante `Shutdown`.

Nessun test automatico: `make test` deve restare verde (i test di `cmd/api` avviano davvero `run`),
poi si verifica dal vivo.

Errori frequenti, visibili solo dal vivo:

- `queue.Start(ctx)` con il context del segnale → al Ctrl+C `"delivered":0` e notifiche perse
  (misurato: 5 pubblicate, 0 consegnate, 4 fallite, 1 sparita senza log);
- `nil` rimasto in `ticket.NewService` → nessun `notification sent` nel log;
- coda chiusa prima dell'HTTP → una richiesta lenta ancora in corso riceve `queue closed` e perde
  la notifica;
- `return` al primo errore in `shutdown` → se l'HTTP sfora la deadline la coda non viene chiusa.

## Evidenze dal vivo

Le variabili si passano **come argomenti di `make`**: `make run NOTIFY_WORKERS=1`. Scritte prima
(`NOTIFY_WORKERS=1 make run`) vengono ignorate in silenzio, perché il `Makefile` legge `.env` e i
suoi valori vincono sull'ambiente.

Terminale 2, preparazione (riusa Ada delle tappe precedenti):

```bash
B=http://localhost:8080
J='Content-Type: application/json'
T=$(curl -s -X POST $B/v1/auth/login -H "$J" -d '{"email":"ada@example.com","password":"correct-horse"}' \
    | sed -E 's/.*"token":"([^"]+)".*/\1/')
many() { for i in $(seq $1); do curl -s -o /dev/null -w '%{http_code} ' -X POST $B/v1/tickets \
         -H "$J" -H "Authorization: Bearer $T" -d '{"title":"carico"}'; done; echo; }
```

Se Ada non esiste: `curl -s -X POST $B/v1/auth/register -H "$J" -d
'{"email":"ada@example.com","password":"correct-horse"}'`. Poi, per ogni prova, avvia il server nel
terminale 1 come indicato e lancia `many N` nel terminale 2:

| Terminale 1 | Terminale 2 | Risultato atteso |
|---|---|---|
| `make run` | `many 1` | 201; dopo circa 200 ms il log `"msg":"notification sent","kind":"created"` |
| `make run NOTIFY_QUEUE_SIZE=1 NOTIFY_WORKERS=1` | `many 10` | tutte 201; alcuni `WARN` `notification not queued` con `"err":"notify: queue full"`; al Ctrl+C `"dropped"` > 0 |
| `make run NOTIFY_WORKERS=1`, Ctrl+C **subito dopo** | `many 5` | `shutdown signal received`, **poi** le 5 `notification sent` (la coda si svuota), poi `shutdown complete` con `"published":5,"delivered":5` |
| `make run NOTIFY_WORKERS=1 SHUTDOWN_TIMEOUT=1s`, Ctrl+C subito dopo | `many 10` | circa 5 consegnate in 1 s, poi `queuedesk stopped` con `notify: shutdown interrupted with N notifications pending: context deadline exceeded`, exit code 1 |
| qualsiasi, Ctrl+C | — | `shutdown complete` con `"notifications":{"published":…,"dropped":…,"delivered":…,"failed":…}` |

<details><summary>Aiuto — livello 1</summary>

- Chi pubblica non deve mai aspettare: se non c'è posto si scarta e si conta.
- Chiusura e invii vanno protetti dallo stesso mutex.
- Un worker ha due motivi per uscire: coda chiusa e vuota, oppure context cancellato.
- Ordine dello shutdown: chi produce notifiche deve fermarsi prima di chi le consuma.
</details>

<details><summary>Aiuto — livello 2</summary>

- `select { case q.ch <- n: … default: … }` è l'invio non bloccante.
- `sync.RWMutex`: `RLock` per gli invii (concorrenti fra loro), `Lock` per la chiusura.
- `q.wg.Go(func() { … })` (Go 1.25) fa da solo `Add` e `Done`.
- WaitGroup con deadline: una goroutine fa `wg.Wait(); close(done)` e tu fai `select` su `done` e
  `ctx.Done()`.
- In `main`: `queue.Start(context.Background())`, non il context del segnale.
</details>

<details><summary>Aiuto — livello 3 (NOTIFY-2, passo B)</summary>

```go
for {
    select {
    case <-ctx.Done():
        return
    case n, ok := <-q.ch:
        if !ok {
            return // coda chiusa e svuotata
        }
        q.deliver(ctx, id, n)
    }
}
```
</details>

<details><summary>Aiuto — livello 3 (NOTIFY-3)</summary>

```go
q.mu.Lock()
if !q.closed { q.closed = true; close(q.ch) }
q.mu.Unlock()

done := make(chan struct{})
go func() { q.wg.Wait(); close(done) }()
select {
case <-done:
    return nil
case <-ctx.Done():
    // cancella i worker, aspetta che escano (<-done),
    // poi restituisci un errore che avvolge ctx.Err()
}
```
</details>

## Fine tappa

Prima del commit verifica questa checklist:

- **NOTIFY-1**: `TryPublish` non blocca mai; coda piena → `ErrQueueFull`, coda chiusa →
  `ErrClosed`; nessuna race con `Shutdown`;
- **NOTIFY-2**: esattamente N worker; ognuno esce a coda chiusa e vuota oppure alla cancellazione;
- **NOTIFY-3**: `Shutdown` idempotente; svuota la coda entro la deadline; oltre la deadline cancella,
  aspetta i worker e restituisce un errore con `ctx.Err()`;
- **NOTIFY-4**: una notifica per ogni operazione riuscita, nessuna per quelle fallite; una coda piena
  non fa fallire le richieste;
- **NOTIFY-5**: ordine HTTP → coda → database, un'unica deadline, statistiche nel log finale;
- `make test`, `make test-race`, `make test-acceptance` e le prove della tabella **Evidenze dal
  vivo** hanno l'esito atteso.

`make test-acceptance` verde, poi `git commit -am "tappa 5"` e passa alla tappa 6 con
`git switch -c lavoro/tappa-6 origin/tappa-6-robustezza`.
