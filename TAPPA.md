# Tappa 6 — Test e robustezza

| Giorno | Durata | Branch | TODO |
|---|---|---|---|
| 2 | 50' | `tappa-6-robustezza` | ROB-1 |

## Prima di iniziare

```bash
git switch -c lavoro/tappa-6 origin/tappa-6-robustezza
```

## Obiettivo

Rendere rigoroso l'input JSON, poi vedere all'opera race detector e profiling sul codice scritto
nelle tappe precedenti. Alla fine tutto è verde: unit test con `-race`, acceptance test e una demo
end-to-end con `curl`.

## Da dove partiamo

Il servizio è completo, notifiche comprese (la soluzione della tappa 5 è nel branch). Resta un punto
debole: `decodeJSON`, usata da ogni handler con un body, accetta qualsiasi cosa:

- qualunque `Content-Type`, anche nessuno;
- campi sconosciuti, ignorati in silenzio: `"role":"agent"` nella register non dà errore, e il
  client crede di aver chiesto qualcosa;
- solo il primo valore: `{…}{…}` e `{…} x` passano;
- body di qualunque dimensione, letti in memoria.

Cosa trovi già pronto, e perché:

| Già pronto | Perché non lo scrivi tu |
|---|---|
| `requestError`, `badRequest`, `maxBodyBytes` in `json.go` | il tipo di errore che `respondErr` traduce in status e codice, e il limite di 64 KiB |
| `jsonError` in `json.go` | traduce ogni errore di `encoding/json` in un messaggio utile; serve solo chiamarla |
| `internal/httpapi/json_test.go` | il test, che usa la register come rotta pubblica |
| `scripts/smoke.sh` (`make smoke`) | demo end-to-end con `curl`: 29 controlli su tutto il servizio |
| `internal/auth/bench_test.go` | i benchmark di bcrypt e SHA-256 per il profiling |

| ID | File | Cosa scrivi | Concetti Go |
|---|---|---|---|
| ROB-1 | `internal/httpapi/json.go` | `decodeJSON` rigoroso: 415, 413, campi sconosciuti, un solo valore | `mime.ParseMediaType`, `http.MaxBytesReader`, `json.Decoder`, `errors.Is`/`errors.As` |

## Metodo di lavoro

Lo stesso ciclo delle tappe precedenti:

1. leggi **dove** viene chiamata la funzione e quale problema risolve;
2. apri `json_test.go`: nomi, body, `Content-Type` e risultati attesi sono il contratto;
3. esegui il test mirato prima della modifica e leggi i subtest rossi;
4. implementa un passo del TODO alla volta e riesegui lo stesso test;
5. alla fine esegui `make check` e l'intero `make test-acceptance`.

Regole: non modificare test, firme, `jsonError` o l'ID `TODO(ROB-1)`.

## Passo 0 — Ambiente e giro del codice (2')

```bash
make test             # baseline verde
make todo             # solo ROB-1
make test-acceptance  # 1 test rosso: TestDecodeJSONHardening
```

Contatore dei test rossi di `make test-acceptance`: 1 → **0** dopo ROB-1. Dentro quel test, 7
subtest rossi su 9.

## Passo 1 — ROB-1: `decodeJSON` rigoroso (18')

```bash
make test-acceptance RUN=TestDecodeJSONHardening
```

Ogni subtest manda un body alla register. La tabella dice cosa succede con lo stub e quale passo
del TODO lo sistema:

| Subtest | Body / `Content-Type` | Con lo stub | Atteso | Lo sblocca |
|---|---|---|---|---|
| `charset parameter accepted` | valido, `application/json; charset=utf-8` | 201 | 201 | verde già con lo stub: deve restarlo |
| `missing content type` | valido, nessun `Content-Type` | 201 | 415 `unsupported_media_type` | passo A |
| `text/plain` | valido, `text/plain` | 201 | 415 `unsupported_media_type` | passo A |
| `form encoded` | `email=a&password=b`, form | 400 `invalid_json` | 415 `unsupported_media_type` | passo A |
| `unknown field` | `{…,"role":"agent"}` | 201 | 400 `unknown_field` | passo B (`DisallowUnknownFields`) |
| `body too large` | un oggetto da 70 KiB | 201 | 413 `body_too_large` | passo B (`MaxBytesReader`) |
| `two JSON values` | `{…}{…}` | 201 | 400 `invalid_json` | passo D |
| `trailing garbage` | `{…} x` | 201 | 400 `invalid_json` | passo D |
| `array instead of object` | `[]` | 400 `invalid_json` | 400 `invalid_json` | verde già con lo stub (`jsonError`) |

Il passo C (il primo `Decode` con `jsonError`) c'è già nello stub, ma su un decoder creato al volo:
va riscritto su `dec`, dopo i passi A e B.

Due errori che il test **non** vede, perché il suo body da 70 KiB è un solo oggetto e quindi lo
ferma già il primo `Decode`:

- nel passo D, l'errore del secondo `Decode` non controllato con `errors.As`: un oggetto valido
  seguito da 70 KiB di spazi dà 400 invece di 413;
- `dec.More()` al posto del secondo `Decode`: accetta un `}` o un `]` di troppo, e anche lui dà 400
  invece di 413.

Li trovi con le ultime due righe della tabella **Evidenze dal vivo**.

## Passo 2 — Race detector (10')

In `internal/notify/queue.go`, dentro `TryPublish`, commenta temporaneamente le due righe
`q.mu.RLock()` e `defer q.mu.RUnlock()`, poi:

```bash
go test -race -run TestConcurrentPublishDuringShutdown -count=20 ./internal/notify
```

Atteso: il test fallisce, di solito con entrambi gli esiti nella stessa esecuzione.

- `WARNING: DATA RACE`, anche due volte: la lettura di `q.closed` in `TryPublish` contro la
  scrittura `q.closed = true` in `Shutdown`, e l'invio `q.ch <- n` contro `close(q.ch)`. Ogni
  rapporto si legge dall'alto: l'accesso corrente (`Read at` / `Write at`), quello precedente
  (`Previous write at` / `Previous read at`), poi dove sono nate le due goroutine. Le righe di
  `queue.go` e `queue_test.go` sono quelle da guardare;
- `panic: send on closed channel`: la race si è manifestata davvero.

Poi ripristina il codice (togli i commenti, oppure `git restore internal/notify/queue.go`) e rilancia
lo stesso comando: `ok`.

## Passo 3 — Profiling (10')

```bash
go test -run '^$' -bench . -benchmem ./internal/auth
go test -run TestRegister -cpuprofile cpu.out ./internal/auth
go tool pprof -top -nodecount=8 cpu.out
rm -f cpu.out auth.test
```

| Comando | Cosa guardare |
|---|---|
| benchmark | `BenchmarkHashPassword` (bcrypt) nell'ordine delle decine di ms per operazione, `BenchmarkHashToken` (SHA-256) nelle decine di ns: circa sei ordini di grandezza. I numeri esatti dipendono dalla CPU |
| `pprof -top` | in cima, con quasi tutto il tempo, `golang.org/x/crypto/blowfish.encryptBlock`: è il cuore di bcrypt. Più sotto `bcrypt.GenerateFromPassword` e `auth.(*Service).Register` |

`-run '^$'` non corrisponde a nessun test: si eseguono solo i benchmark. Con `-cpuprofile`, `go
test` lascia nella cartella corrente anche il binario del test, `auth.test`, che `.gitignore` non
esclude: per questo l'ultimo comando lo cancella, insieme a `cpu.out`.

Domanda: il login è lento, va ottimizzato? (Risposta in plenaria.)

## Passo 4 — Tutto verde (10')

```bash
make check               # fmt + vet + test-race: tutto ok
make test-acceptance     # tutto ok
make run                 # terminale 1
make smoke               # terminale 2: "== 29 ok, 0 falliti"
```

`make smoke` richiede l'agent demo (`make seed`) e registra ogni volta utenti nuovi: si può
rilanciare quante volte si vuole. Prima di ROB-1 finisce con `== 27 ok, 2 falliti`: sono
`register con ruolo agent -> unknown_field` (201 invece di 400) e `body non JSON -> 415` (400
invece di 415).

## Evidenze dal vivo

Con `make run` nel terminale 1, nel terminale 2:

```bash
B=http://localhost:8080/v1/auth/register
J='Content-Type: application/json'
V='{"email":"rob1@example.com","password":"correct-horse"}'
P=$(head -c 71680 /dev/zero | tr '\0' x)    # 70 KiB di "x"
```

"Accettato" vuol dire che il body è stato decodificato e la register è andata avanti: 201 la prima
volta, poi 409 `email_taken`.

| Comando | Prima di ROB-1 | Dopo ROB-1 |
|---|---|---|
| `curl -s $B -H 'Content-Type:' -d "$V"` | accettato | 415 `unsupported_media_type` |
| `curl -s $B -H 'Content-Type: text/plain' -d "$V"` | accettato | 415 `unsupported_media_type` |
| `curl -s $B -d 'email=a&password=b'` | 400 `malformed JSON at byte 1` | 415 `unsupported_media_type` |
| `curl -s $B -H 'Content-Type: application/json; charset=utf-8' -d "$V"` | accettato | accettato |
| `curl -s $B -H "$J" -d '{"email":"rob2@example.com","password":"correct-horse","role":"agent"}'` | 201 con `"role":"requester"`: il campo è ignorato | 400 `unknown_field`, `unknown field "role"` |
| `curl -s $B -H "$J" -d "$V$V"` | accettato: registra il primo, ignora il secondo | 400 `body must contain a single JSON object` |
| `curl -s $B -H "$J" -d "$V x"` | accettato | 400 `body must contain a single JSON object` |
| `curl -s $B -H "$J" -d "{\"email\":\"rob3@example.com\",\"password\":\"$P\"}"` | 400 `invalid_input` (password oltre 72 byte): il body è stato letto tutto | 413 `body must not exceed 65536 bytes` |
| `curl -s $B -H "$J" -d "$V}"` | accettato | 400 `body must contain a single JSON object` (con `dec.More()`: accettato) |
| `curl -s $B -H "$J" -d "$V$(printf '%70000s' '')"` | accettato | 413 `body_too_large` (senza `errors.As` nel passo D: 400) |

<details><summary>Aiuto — livello 1</summary>

`encoding/json` è permissivo per default: ignora i campi sconosciuti e si ferma al primo valore.
Quali opzioni del `Decoder` cambiano questo comportamento? E cosa deve restituire un secondo
`Decode` se il body conteneva un solo oggetto?
</details>

<details><summary>Aiuto — livello 2</summary>

- `mime.ParseMediaType(r.Header.Get("Content-Type"))` restituisce il media type senza parametri.
- `http.MaxBytesReader(w, r.Body, maxBodyBytes)` va assegnato a `r.Body` **prima** di
  `json.NewDecoder(r.Body)`; poi `dec.DisallowUnknownFields()`.
- Un secondo `dec.Decode(&struct{}{})` deve restituire `io.EOF`.
- `jsonError` traduce già tutti gli errori, compreso `*http.MaxBytesError` → 413.
</details>

<details><summary>Aiuto — livello 3 (passo D)</summary>

```go
if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
    var maxErr *http.MaxBytesError
    if errors.As(err, &maxErr) {
        return jsonError(err) // limite superato dopo il primo oggetto: 413
    }
    return badRequest("invalid_json", "body must contain a single JSON object")
}
return nil
```
</details>

## Fine laboratorio

Prima del commit verifica questa checklist:

- **ROB-1**: senza `Content-Type` JSON → 415, con `charset` → accettato; campi sconosciuti → 400
  `unknown_field`; un solo valore JSON, altrimenti 400; oltre 64 KiB → 413, anche quando il limite
  si supera dopo il primo oggetto;
- il race detector ha trovato la race del Passo 2, e `queue.go` è tornato com'era;
- nessun `auth.test` o `cpu.out` rimasto nella cartella;
- `make check`, `make test-acceptance` e `make smoke` (29 ok) sono verdi.

Quando il docente pubblica la soluzione completa, confrontala con il tuo lavoro:

```bash
git fetch origin soluzione:soluzione
git diff lavoro/tappa-6 soluzione
```

Per proseguire: `BACKLOG.md`.
