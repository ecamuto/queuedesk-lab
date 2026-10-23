# Tappa 1 — Fondamenta del servizio

| Giorno | Durata | Branch | TODO |
|---|---|---|---|
| 1 | 50' | `tappa-1-setup` | BASE-1 … BASE-4 |

## Prima di iniziare

```bash
git switch -c lavoro/tappa-1 origin/tappa-1-setup
cp .env.example .env
```

## Obiettivo

Scrivere in Go le fondamenta di un servizio HTTP: configurazione validata, health check, un
middleware e un `main` che si avvia e si chiude in modo ordinato. Alla fine `make run` risponde su
`:8080` e Ctrl+C chiude tutto senza perdere richieste.

## Da dove partiamo

Il branch **non** contiene un servizio funzionante: `make run` oggi si ferma subito con un errore
`TODO(BASE-4)`. Cosa trovi già pronto, e perché:

| Già pronto | Perché non lo scrivi tu |
|---|---|
| `compose.yaml`, `Makefile`, `.env.example` | infrastruttura, non Go |
| `migrations/` e `cmd/migrate` | lo schema SQL completo: lo leggi, non lo scrivi |
| `internal/postgres/db.go` | `Open` crea il pool pgx: una riga di libreria |
| `internal/httpapi/json.go`, `errors.go` | `writeJSON` e `writeError`, da usare |
| `logRequests`, `recoverPanics` in `middleware.go` | modelli da imitare per BASE-3 |

| ID | File | Cosa scrivi | Concetti Go |
|---|---|---|---|
| BASE-1 | `internal/config/config.go` | `duration`, `integer`, errori aggregati | `time.ParseDuration`, `strconv`, `errors.Join`, funzioni come parametri |
| BASE-2 | `internal/httpapi/server.go`, `health.go` | route e handler di `/healthz` e `/readyz` | `ServeMux` con metodo nel pattern, interfacce implicite, `context.WithTimeout` |
| BASE-3 | `internal/httpapi/middleware.go` | middleware request ID | closure, `http.Handler`, `context.WithValue`, type assertion |
| BASE-4 | `cmd/api/main.go` | avvio e shutdown ordinato | goroutine, canali, `select`, `signal.NotifyContext` |

Nel codice, i commenti che iniziano con `// Go:` spiegano il linguaggio (sintassi, idiomi,
perché si scrive così); gli altri spiegano il servizio. Leggili: fanno parte della tappa.

Ordine consigliato: BASE-1 → BASE-2 → BASE-3 si verificano con unit test, senza avviare nulla;
BASE-4 si verifica anche dal vivo. I restanti 13' sono concetti alla lavagna, evidenze e debrief.

## Metodo di lavoro per ogni TODO

Non partire scrivendo codice a memoria. Usa sempre questo ciclo breve:

1. leggi **dove** viene chiamata la funzione e quale problema risolve;
2. apri il relativo `_test.go`: nomi, input e risultati attesi sono il contratto;
3. esegui il test mirato prima della modifica e leggi il primo fallimento;
4. implementa un solo passo A/B/C e rimuovi soltanto lo stub indicato;
5. esegui di nuovo lo stesso test; poi passa al passo successivo;
6. alla fine esegui `make test` e l'intero `make test-acceptance`.

Regole:

- non modificare test, firme delle funzioni, messaggi richiesti o ID `TODO(...)`;
- aggiungi/rimuovi import quando richiesto: Go non accetta import inutilizzati;
- usa gli helper già presenti (`p.fail`, `writeJSON`, `writeError`, `newRequestID`);
- un test rosso è informazione: confronta `got` e `want`, poi risali alla funzione responsabile;
- se il codice non compila, risolvi prima quello: nessun test può partire finché il package non compila.

## Passo 0 — Ambiente e giro del repo (3')

```bash
make db-up          # attende l'healthcheck; `docker compose ps` mostra "(healthy)"
make migrate-up     # up 0001_auth / up 0002_tickets
make test           # baseline verde
make run            # si ferma con "TODO(BASE-4) serve": è normale
make todo           # i punti da completare
```

Apri `cmd/api/main.go` e segui `run` dall'alto: config → pool → handler → server. È la mappa della
tappa.

## Passo 1 — BASE-1: configurazione (10')

**Dove entra nel servizio.** `make run` carica `.env` tramite il Makefile; `main` passa `os.Getenv`
a `config.Load`; il risultato configura connessione PostgreSQL, server HTTP, durata sessioni e coda
notifiche. Questo è il primo codice applicativo eseguito all'avvio.

**Perché serve.** Le variabili d'ambiente sono stringhe, mentre il programma vuole tipi validati
come `time.Duration` e `int`. Il servizio deve fallire subito, prima di aprire porte o connessioni, e
mostrare tutti gli errori insieme: una configurazione parzialmente valida non deve arrivare al runtime.

**Come svilupparlo.** Segui il dato `SESSION_TTL`: `.env` → `os.Getenv` → `Load` → `p.duration` →
campo `Config.SessionTTL`. Poi completa i tre passi sotto senza modificare i test.

`config.Load` riceve `getenv func(string) string` invece di chiamare `os.Getenv`: nei test si passa
una funzione su una mappa. `str` è già scritta: usala come modello per `duration` e `integer`.

Lavora in questo ordine dentro `internal/config/config.go`:

1. **Passo A — `duration`**: leggi la stringa, usa il default se vuota, convertila con
   `time.ParseDuration`, registra formato e intervallo errati con `p.fail`.
2. **Passo B — `integer`**: stesso flusso esplicito, ma conversione con `strconv.Atoi`.
3. **Passo C — `Load`**: unisci tutti gli errori accumulati in `p.errs`; non restituire soltanto il
   primo.

Prima di scrivere, individua tre elementi già pronti: `str` mostra come leggere un default, `fail`
mostra come accumulare un errore, `TestLoadErrors` mostra il risultato finale atteso. Non modificare
i test. Dopo ogni passo puoi rilanciare lo stesso comando: il numero di casi falliti deve diminuire.

### Mappa della configurazione

| Variabile | Tipo finale | Default | Validazione | Da scrivere? |
|---|---|---|---|---|
| `DATABASE_URL` | `string` | nessuno | obbligatoria, schema PostgreSQL, host presente | no: `str` + `checkDatabaseURL` pronti |
| `HTTP_ADDR` | `string` | `:8080` | formato `host:porta` | no: `str` + `checkAddr` pronti |
| `SESSION_TTL` | `time.Duration` | `24h` | da `1m` a `720h` | sì: usa `duration` |
| `SHUTDOWN_TIMEOUT` | `time.Duration` | `10s` | da `1s` a `5m` | sì: usa `duration` |
| `NOTIFY_WORKERS` | `int` | `4` | da 1 a 64 | sì: usa `integer` |
| `NOTIFY_QUEUE_SIZE` | `int` | `64` | da 1 a 10000 | sì: usa `integer` |

`Load` costruisce comunque tutti i campi e richiama tutte le validazioni. `p.fail` aggiunge errori a
`p.errs` senza interrompere il flusso; solo alla fine il passo C decide se restituire `cfg` oppure un
errore aggregato.

### Comportamento richiesto dei parser

| Input | `duration` | `integer` | Effetto su `p.errs` |
|---|---|---|---|
| stringa vuota | restituisce `def` | restituisce `def` | nessuno |
| valore valido nell'intervallo | durata convertita | intero convertito | nessuno |
| formato errato (`24`, `four`) | restituisce `0` | restituisce `0` | aggiunge errore di formato |
| valore valido ma fuori intervallo | restituisce valore convertito | restituisce valore convertito | aggiunge errore di intervallo |

Il valore restituito dopo un errore non verrà usato: il passo C scarta l'intera `Config`. Serve però
restituire qualcosa perché le firme dei parser restituiscono un valore, non `(valore, error)`.

### Sviluppo e test incrementali

```bash
# Deve già essere verde: verifica default e codice fornito.
make test-acceptance RUN=TestLoadDefaults

# Implementa passi A e B: conversioni e valori personalizzati.
make test-acceptance RUN=TestLoadCustomValues

# Verifica formati, limiti e aggregazione del passo C.
make test-acceptance RUN=TestLoadErrors

# Isola proprio il caso che richiede errors.Join.
make test-acceptance RUN='TestLoadErrors/all_errors_reported_together'
```

Errori frequenti:

- `time.ParseDuration("24")` fallisce: manca l'unità; non aggiungere secondi implicitamente;
- `strconv.Atoi(" 8")` fallisce: non fare `TrimSpace`, la configurazione deve essere esplicita;
- fare `return` al primo `p.fail` impedisce di mostrare tutti gli errori;
- usare `p.errs[0]` lascia rosso il caso `all_errors_reported_together`;
- inserire `DATABASE_URL` nel messaggio può mostrare la password: i controlli esistenti non lo fanno.

```bash
make test-acceptance RUN=TestLoad
```

## Passo 2 — BASE-2: route e health (7')

**Dove entra nel servizio.** `main` chiama `httpapi.NewHandler`; qui il `ServeMux` collega URL e
funzioni. Il relativo handler viene poi passato a `http.Server`. `/healthz` e `/readyz` sono chiamati
da operatore, load balancer o orchestratore, non dall'interfaccia utente.

**Perché serve.** La liveness risponde alla domanda “il processo è vivo?”; la readiness a “può
servire traffico adesso?”. Separarle evita di riavviare un processo sano solo perché PostgreSQL è
temporaneamente indisponibile.

**Come svilupparlo.** Prima registra le route, poi implementa la risposta indipendente dal DB, infine
aggiungi il controllo DB con timeout. Verifica ogni comportamento separatamente.

Qui ci sono tre modifiche piccole in due file:

1. **Passo A**, `server.go`: registra le due coppie metodo/percorso → handler.
2. **Passo B**, `health.go`: `/healthz` restituisce sempre 200; non usa il DB.
3. **Passo C**, `health.go`: `/readyz` prova `a.db.Ping` con timeout; 200 se riesce, 503 se fallisce.

Prima fai passare `TestHealthz`, poi `TestReadyz`: separare i due obiettivi rende più chiaro dove
cercare un eventuale errore.

### Componenti coinvolti

| File | Elemento | Responsabilità |
|---|---|---|
| `server.go` | `Pinger` | contratto minimo per verificare il DB |
| `server.go` | `Deps.DB` | dipendenza reale in produzione, finta nei test |
| `server.go` | `NewHandler` | crea mux, registra route, applica middleware |
| `health.go` | `healthz` | liveness indipendente dal DB |
| `health.go` | `readyz` | readiness tramite `Ping` con timeout |
| `json.go` / `errors.go` | `writeJSON`, `writeError` | formato uniforme delle risposte |

In produzione `Deps.DB` contiene `*pgxpool.Pool`. Nei test contiene `fakePinger`. Entrambi hanno
`Ping(context.Context) error`, quindi soddisfano `Pinger` senza dichiarazioni esplicite.

### Contratto HTTP da ottenere

| Richiesta | Stato DB | Risposta attesa | Note |
|---|---|---|---|
| `GET /healthz` | disponibile o guasto | 200 `{"status":"ok"}` | non deve chiamare `Ping` |
| `POST /healthz` | qualsiasi | 405 | ottenuto dal pattern `GET /healthz` |
| `GET /readyz` | disponibile | 200 `{"status":"ready"}` | `Ping` riuscito |
| `GET /readyz` | guasto/lento | 503 `not_ready` | dettaglio nel log, non nel body |

`readyz` deriva il timeout da `r.Context()`: se il client abbandona la richiesta, anche `Ping` viene
cancellato; in ogni caso non può superare `readyTimeout`.

### Sviluppo e test incrementali

```bash
# Passo A: route e metodo HTTP.
make test-acceptance RUN='TestHealthzMethodNotAllowed'

# Passo B: liveness, anche con pinger guasto.
make test-acceptance RUN='^TestHealthz$'

# Passo C: entrambi gli esiti della readiness.
make test-acceptance RUN='^TestReadyz$'
```

Errori frequenti:

- pattern `/healthz` senza `GET `: POST viene accettato invece di ricevere 405;
- chiamare `Ping` in `healthz`: un DB guasto rende falsa anche la liveness;
- dimenticare `defer cancel()`: il timer resta allocato fino alla scadenza;
- dimenticare `return` dopo il 503: handler prova a scrivere anche il successivo 200;
- restituire l'errore pgx al client: dettagli interni devono restare solo nei log.

```bash
make test-acceptance RUN='TestHealthz|TestReadyz'
```

`Pinger` (in `server.go`) ha un solo metodo. `*pgxpool.Pool` lo soddisfa senza dichiararlo, e nei
test lo sostituisce `fakePinger`: guarda `helpers_test.go`.

## Passo 3 — BASE-3: middleware request ID (7')

**Dove entra nel servizio.** `NewHandler` avvolge tutto il mux con `requestID`; quindi ogni richiesta,
presente e futura, passa da questo codice. L'ID finisce nell'header di risposta, nel context e nei log
scritti tramite `LogHandler`.

**Perché serve.** Più richieste girano contemporaneamente. Senza un identificatore comune è difficile
riconoscere quali righe di log appartengano alla stessa richiesta o confrontare log client e server.

**Come svilupparlo.** Implementa prima la piccola funzione che legge dal context. Poi costruisci il
wrapper HTTP seguendo il dato dall'header in ingresso fino a `next.ServeHTTP`.

Segui il flusso di un ID attraverso una richiesta:

```text
header in ingresso → valida/genera ID → header in uscita → context → handler e log
```

Implementa prima `RequestIDFrom` (**passo A**), poi `requestID` (**passo B**). Nel secondo non basta
creare il nuovo context: devi passare a `next` la richiesta restituita da `r.WithContext(ctx)`.

### Contratto del middleware

| Header in ingresso | Azione | Header in risposta | Valore nel context/log |
|---|---|---|---|
| assente | genera 8 byte casuali, codificati hex | nuovo ID di 16 caratteri | stesso nuovo ID |
| `abc-123` | riusa valore valido | `abc-123` | `abc-123` |
| oltre 64 caratteri o caratteri non ammessi | scarta e genera nuovo ID | nuovo ID | stesso nuovo ID |

La regex già pronta accetta lettere, cifre, `.`, `_` e `-`, da 1 a 64 caratteri. Non devi modificare
né regex né `newRequestID`.

### Perché servono context e closure

- `requestID(next)` viene chiamata una volta quando si compone il server;
- la funzione anonima restituita viene chiamata per ogni richiesta;
- la closure conserva `next`, quindi può continuare la catena;
- `context.WithValue` crea un nuovo context: non modifica quello originale;
- `r.WithContext` crea una copia della richiesta: devi passare quella copia a `next`;
- la chiave privata `ctxKey` evita collisioni con valori inseriti da altri package.

Ordine rilevante: `requestID` è il middleware più esterno, quindi inserisce l'ID prima che partano
logging, recovery e handler.

### Sviluppo e test incrementali

```bash
# Passo A + propagazione nel context fino al logger.
make test-acceptance RUN=TestRequestIDInLogs

# Passo B: generazione, riuso e sostituzione degli ID non validi.
make test-acceptance RUN='^TestRequestID$'
```

Errori frequenti:

- `ctx.Value(...).(string)` con un solo risultato: panic quando valore manca;
- impostare header dopo `next.ServeHTTP`: risposta potrebbe essere già iniziata;
- creare `ctx` ma chiamare `next.ServeHTTP(w, r)`: ID non arriva al logger;
- restituire `next` senza wrapper: middleware compila ma non esegue nulla;
- fidarsi di qualunque ID client: stringhe enormi o caratteri di controllo finiscono nei log.

```bash
make test-acceptance RUN=TestRequestID
```

## Passo 4 — BASE-4: avvio e shutdown (10')

**Dove entra nel servizio.** `main` chiama `run`, che mantiene vivo l'intero processo. In locale il
segnale arriva da Ctrl+C; in container o produzione arriva normalmente come SIGTERM. `shutdown`
coordina la chiusura del server prima che `run` chiuda il pool PostgreSQL con il `defer`.

**Perché serve.** `ListenAndServe` blocca, quindi deve convivere con l'attesa dei segnali. Alla
chiusura non vogliamo accettare nuove richieste né troncare quelle già in corso; serve però una
deadline, altrimenti una richiesta bloccata impedirebbe al processo di terminare.

**Come svilupparlo.** Separa avvio concorrente e chiusura ordinata. Fai passare prima il caso “porta
occupata”, poi il caso “segnale ricevuto”: insieme provano entrambi i rami del `select`.

Questo è il TODO più grande. Trattalo come due problemi separati:

1. **Passo A — `run`**: una goroutine esegue `ListenAndServe`; il flusso principale attende con
   `select` un errore del server oppure `ctx.Done()`.
2. **Passo B — `shutdown`**: crea un context nuovo con timeout e passalo a `srv.Shutdown`.

Disegno del passo A:

```text
goroutine server ──errore──▶ serveErr ─┐
                                      ├─▶ select ─▶ shutdown
SIGINT/SIGTERM ───────────▶ ctx.Done ─┘
```

Fai prima passare `TestRunPortInUse`: dimostra che leggi `serveErr`. Poi
`TestRunGracefulShutdown`: dimostra che leggi `ctx.Done()` e chiudi correttamente.

### Cosa esiste già prima del TODO

Quando l'esecuzione arriva allo stub, `run` ha già:

1. caricato e validato `Config`;
2. creato un context cancellabile da SIGINT/SIGTERM;
3. aperto e verificato il pool PostgreSQL;
4. composto handler e `http.Server`;
5. registrato `defer pool.Close()`.

Devi quindi gestire solo il ciclo di vita del server. Non spostare config, connessione o costruzione
degli handler dentro la goroutine.

### Eventi che `run` deve distinguere

| Evento | Canale/caso | Comportamento |
|---|---|---|
| porta libera | `ListenAndServe` resta bloccata | server continua a servire |
| porta occupata/errore listener | `serveErr` riceve errore | `run` avvia shutdown e restituisce errore contestualizzato |
| Ctrl+C o SIGTERM | `ctx.Done()` diventa pronto | log del segnale, poi shutdown ordinato |
| `Shutdown` ferma normalmente server | `http.ErrServerClosed` | non deve diventare errore applicativo |
| deadline shutdown scaduta | `srv.Shutdown` restituisce errore | `run` restituisce `http shutdown: ...` |

Perché `serveErr` ha buffer 1: se segnale ed errore arrivano insieme, `select` può scegliere il
segnale. La goroutine server deve comunque poter inviare l'errore e terminare senza restare bloccata.

Perché `shutdown` usa un context nuovo: quello creato da `signal.NotifyContext` è precisamente il
context appena cancellato. Passarlo a `srv.Shutdown` farebbe terminare subito l'attesa delle richieste.

Ordine di chiusura finale:

```text
stop nuove richieste HTTP → attendi richieste in corso → ritorna da run → chiudi pool PostgreSQL
```

### Sviluppo e test incrementali

```bash
# Richiede PostgreSQL: make db-up e make migrate-up.

# Verifica ramo serveErr: seconda istanza/porta occupata.
make test-acceptance RUN=TestRunPortInUse

# Verifica ramo ctx.Done e shutdown senza errore.
make test-acceptance RUN=TestRunGracefulShutdown

# Entrambi.
make test-acceptance RUN=TestRun
```

Errori frequenti:

- chiamare `ListenAndServe` senza `go`: `run` non arriva mai al `select`;
- inviare anche `http.ErrServerClosed`: Ctrl+C termina con exit code 1;
- canale non bufferizzato: possibile goroutine bloccata durante shutdown;
- attendere solo `ctx.Done`: errore di bind non viene mai restituito;
- attendere solo `serveErr`: Ctrl+C non avvia shutdown;
- usare context del segnale in `Shutdown`: chiusura immediata invece che graceful;
- chiamare `os.Exit` dentro `run`: salta i `defer`, rende difficile il test.

```bash
make test-acceptance RUN=TestRun      # avvia run davvero e le manda SIGINT
make run
```

**Evidenze dal vivo**

| Prova | Risultato atteso |
|---|---|
| `curl -s localhost:8080/healthz` | `{"status":"ok"}` |
| `curl -si -X POST localhost:8080/healthz` | `405 Method Not Allowed` |
| `curl -s localhost:8080/readyz` | `{"status":"ready"}` |
| `make db-down`, poi `/readyz` e `/healthz` | `/readyz` 503 `not_ready`, `/healthz` sempre 200 (poi `make db-up`) |
| `curl -si localhost:8080/healthz \| grep -i x-request-id` | un request ID generato |
| `curl -si -H 'X-Request-ID: abc' localhost:8080/healthz` | lo stesso `abc`, anche nella riga di log |
| `DATABASE_URL= SESSION_TTL=24 go run ./cmd/api` | errori su `DATABASE_URL` **e** `SESSION_TTL` insieme, poi uscita |
| Ctrl+C su `make run` | `shutdown signal received`, `shutdown complete`, exit 0 |
| due `make run` in due terminali | il secondo esce con `http server: listen tcp :8080: bind: address already in use` |

<details><summary>Aiuto — livello 1</summary>

- BASE-1: `p.fail` non ferma niente, accumula. Il valore restituito dopo un errore conta poco: conta che l'errore sia in `p.errs`.
- BASE-2: la liveness non deve dipendere da nulla di esterno. La readiness sì.
- BASE-3: un middleware riceve un handler e ne restituisce un altro che "gli sta attorno".
- BASE-4: `ListenAndServe` blocca finché il server vive. Chi aspetta il segnale, intanto?
</details>

<details><summary>Aiuto — livello 2</summary>

- `time.ParseDuration("24")` fallisce (manca l'unità): è proprio l'errore da segnalare.
- Gli import (`errors`, `strconv`) li aggiunge l'editor; altrimenti `goimports`.
- `mux.HandleFunc("GET /healthz", a.healthz)`: `a.healthz` è un *method value*, una funzione legata ad `a`.
- `id, _ := ctx.Value(requestIDKey).(string)`: con `_` al posto di `ok` niente panic se manca.
- `select` aspetta il primo di più canali pronti; `ctx.Done()` è un canale.
- `srv.Shutdown` vuole un context **nuovo**: quello di `run` è già cancellato dal segnale.
</details>

<details><summary>Aiuto — livello 3 (BASE-4)</summary>

```go
serveErr := make(chan error, 1)
go func() {
    log.Info("http server listening", "addr", cfg.HTTPAddr)
    if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
        serveErr <- err
    }
    close(serveErr)
}()

var runErr error
select {
case err := <-serveErr:
    runErr = fmt.Errorf("http server: %w", err)
case <-ctx.Done():
    log.Info("shutdown signal received")
}
stop()
return errors.Join(runErr, shutdown(log, cfg.ShutdownTimeout, srv))
```
</details>

## Fine tappa

Prima del commit verifica questa checklist:

- **BASE-1**: default e valori personalizzati sono caricati; formato e limiti errati producono tutti
  gli errori insieme; nessun messaggio espone la password del database;
- **BASE-2**: liveness resta 200 anche senza DB; readiness passa da 200 a 503 quando PostgreSQL non
  risponde; un metodo HTTP non previsto riceve 405;
- **BASE-3**: l'ID valido del client viene conservato; quello assente o non valido viene sostituito;
  risposta, context e log mostrano lo stesso valore;
- **BASE-4**: errore di bind e segnale sono entrambi gestiti; Ctrl+C termina entro la deadline senza
  trasformare `http.ErrServerClosed` in errore;
- `make test`, `make test-acceptance` e le prove della tabella **Evidenze dal vivo** hanno l'esito
  atteso.

`make test-acceptance` verde, poi `git commit -am "tappa 1"` e passa alla tappa 2 con
`git switch -c lavoro/tappa-2 origin/tappa-2-auth`.
