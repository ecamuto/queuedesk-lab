# Tappa 2 — Autenticazione

| Giorno | Durata | Branch | TODO |
|---|---|---|---|
| 1 | 55' | `tappa-2-auth` | AUTH-1 … AUTH-4 |

## Prima di iniziare

```bash
git switch -c lavoro/tappa-2 origin/tappa-2-auth
make db-up migrate-up   # se il DB era spento; le migrazioni sono le stesse della tappa 1
make seed               # crea l'agent demo agent@queuedesk.local / agent-demo-password
```

`make seed` si può ripetere: la seconda volta stampa `user agent@queuedesk.local already exists:
nothing changed`.

## Obiettivo

Registrazione, login con token opaco, sessioni revocabili e un middleware che distingua bene
"non so chi sei" (401) da "il sistema ha un problema" (500). Alla fine un client fa login, usa il
token su `/v1/me`, fa logout, e da quel momento lo stesso token non vale più.

## Da dove partiamo

Il branch contiene la soluzione della tappa 1, quindi da qui in poi `make run` **si avvia**. La
registrazione funziona già; login e rotte protette rispondono `501 not_implemented` con l'ID del
TODO da completare:

| Richiesta, oggi | Risposta |
|---|---|
| `POST /v1/auth/register` | 201, utente requester creato |
| `POST /v1/auth/login` | 501 `TODO(AUTH-2) Login: unsupported operation` |
| `GET /v1/me` | 501 `TODO(AUTH-4): requireAuth` |

Cosa trovi già pronto, e perché:

| Già pronto | Perché non lo scrivi tu |
|---|---|
| `internal/auth/auth.go` | tipi (`User`, `Session`, `Principal`, `LoginResult`) ed errori sentinella: il vocabolario del package |
| `internal/auth/password.go` | bcrypt, `CheckPassword`, `dummyHash`: libreria più due regole sui limiti |
| `Register` in `internal/auth/service.go` | modello da imitare: normalizza, valida, hash, store, `%w` |
| `internal/postgres/users.go`, `sessions.go` | SQL già scritto. Leggili: traducono `23505` in `ErrEmailTaken` e `pgx.ErrNoRows` in `ErrNotFound` |
| `internal/httpapi/auth_handlers.go` | handler e DTO di register, login, logout e `/v1/me` |
| `withPrincipal`, `principalFrom`, `unauthorized` | accesso al context e risposta 401: li usi in AUTH-4 |
| `respondErr` in `internal/httpapi/errors.go` | l'unico punto che traduce errori di dominio in status HTTP |
| `cmd/seed` | l'unico modo di creare un agent: la register crea sempre requester |

| ID | File | Cosa scrivi | Concetti Go |
|---|---|---|---|
| AUTH-1 | `internal/auth/token.go` | `HashToken`, `NewToken` | `crypto/rand`, `encoding/base64`, array contro slice (`sum[:]`), risultati con nome |
| AUTH-2 | `internal/auth/service.go` | `Login` | errori sentinella, `==` contro `errors.Is`, `%w`, `switch` senza espressione, orologio iniettato |
| AUTH-3 | `internal/auth/service.go` | `Authenticate`, `Logout` | tradurre errori fra layer, idempotenza |
| AUTH-4 | `internal/httpapi/auth_middleware.go` | `bearerToken`, `requireAuth` | `strings.Cut`, `strings.EqualFold`, middleware per singola rotta, valori nel context |

Ordine consigliato: AUTH-1 → AUTH-2 → AUTH-3 si verificano con unit test su fake in memoria, senza
avviare nulla; AUTH-4 si verifica con `httptest` e poi dal vivo con `curl`. I 18' restanti sono
concetti alla lavagna (8' all'inizio), evidenze collettive e debrief (10' alla fine).

### Il percorso di una richiesta

```text
POST /v1/auth/login {email, password}
  → login (handler) → Login (AUTH-2)
      → CredentialsByEmail → CheckPassword → NewToken (AUTH-1) → CreateSession(SHA-256(token))
  ← 200 {"token": "<43 caratteri>", "token_type": "Bearer", ...}

GET /v1/me   Authorization: Bearer <token>
  → requireAuth (AUTH-4) → bearerToken → Authenticate (AUTH-3)
      → HashToken(token) → PrincipalBySession
  → withPrincipal(ctx) → me (handler) → principalFrom(ctx)
  ← 200 {"id": ..., "email": ..., "role": ...}
```

Il token in chiaro esiste solo in due posti: nella risposta del login e nell'header delle richieste
successive. Il database conosce soltanto `SHA-256(token)`.

## Metodo di lavoro per ogni TODO

Lo stesso ciclo della tappa 1:

1. leggi **dove** viene chiamata la funzione e quale problema risolve;
2. apri il relativo `_test.go`: nomi, input e risultati attesi sono il contratto;
3. esegui il test mirato prima della modifica e leggi il primo fallimento;
4. implementa un solo passo A/B/C e rimuovi soltanto lo stub indicato;
5. esegui di nuovo lo stesso test; poi passa al passo successivo;
6. alla fine esegui `make test` e l'intero `make test-acceptance`.

Regole:

- non modificare test, fake, firme delle funzioni, messaggi richiesti o ID `TODO(...)`;
- aggiungi/rimuovi import quando richiesto: Go non accetta import inutilizzati;
- usa gli helper già presenti (`CheckPassword`, `dummyHash`, `HashToken`, `withPrincipal`,
  `unauthorized`, `serverError`);
- un errore "di sicurezza" spesso non rompe nessun test: le regole marcate *nessun test lo scopre*
  vanno rispettate leggendo, non provando;
- non loggare mai password, token o hash: neanche "per debug".

Due test sono **già verdi con lo stub**, perché controllano solo che un guasto dello store non
diventi un errore di credenziali: `TestLoginStoreFailureIsNotACredentialError` e
`TestAuthenticateStoreFailureIsInternal`. Devono restare verdi anche dopo la tua implementazione.

## Passo 0 — Ambiente e giro del codice (2')

```bash
make test             # baseline verde
make todo             # i punti da completare: AUTH-1 … AUTH-4
make test-acceptance  # 12 test rossi: quelli della tabella sotto
```

| File di test | Test rossi | Li sblocca |
|---|---|---|
| `internal/auth/token_test.go` | `TestNewToken`, `TestNewTokenIsUnique` | AUTH-1 |
| `internal/auth/session_test.go` | `TestLoginRejectsBadCredentialsGenerically`, `TestLoginCreatesSessionWithHashOnly` | AUTH-1, AUTH-2 |
| `internal/auth/session_test.go` | `TestAuthenticate`, `TestLogoutRevokesSession` | AUTH-1 … AUTH-3 |
| `internal/postgres/sessions_test.go` | `TestSessionRepoExpiryAndRevocation`, `TestSessionsAreDeletedWithTheirUser` | AUTH-1 (usano `NewToken`) |
| `internal/postgres/sessions_test.go` | `TestAuthServiceWithPostgres` | AUTH-1 … AUTH-3, con PostgreSQL vero |
| `internal/httpapi/auth_middleware_test.go` | `TestRequireAuth`, `TestMe`, `TestLogout` | AUTH-4 |

Contatore dei test rossi di `make test-acceptance` mentre procedi: 12 → **8** dopo AUTH-1 →
**6** dopo AUTH-2 → **3** dopo AUTH-3 → **0** dopo AUTH-4.

Apri `internal/auth/service.go` e leggi `Register`: è il modello. Poi `internal/auth/fakes_test.go`:
`fakeUsers`, `fakeSessions` e `fakeClock` sostituiscono database e orologio nei test di AUTH-2 e
AUTH-3. `fakeSessions.err` simula il database irraggiungibile.

## Passo 1 — AUTH-1: token opaco (5')

**Dove entra nel servizio.** `Login` chiama `NewToken` dopo aver verificato la password: il token va
al client, l'hash nella tabella `sessions`. `Authenticate` e `Logout` chiamano `HashToken` per
ritrovare la sessione a partire dal token ricevuto nell'header.

**Perché serve.** Il token *è* la credenziale: chi lo possiede è l'utente finché la sessione vive.
Deve essere imprevedibile e non deve essere ricostruibile da ciò che sta nel database.

**Come svilupparlo.** Prima `HashToken` (passo A, tre righe), poi `NewToken` (passo B), che la usa.

### Proprietà del token

| Proprietà | Valore | Perché |
|---|---|---|
| sorgente | `crypto/rand` | generatore del sistema operativo: imprevedibile |
| entropia | 32 byte = 256 bit (`TokenBytes`) | impossibile da indovinare o enumerare |
| codifica | `base64.RawURLEncoding` | alfabeto `A-Z a-z 0-9 - _`, niente `=`: sicuro in header e URL |
| lunghezza | 43 caratteri | 32 byte × 8 bit / 6 bit per carattere, arrotondato per eccesso |
| nel database | `SHA-256(token)`, 32 byte | chi legge il DB non ottiene token utilizzabili |

**Perché SHA-256 e non bcrypt.** bcrypt è lento di proposito, per resistere ai tentativi a
dizionario su password scelte da persone. Un token con 256 bit casuali non ha dizionario: un hash
veloce basta, e ogni richiesta autenticata lo ricalcola, quindi deve costare poco (vedi il benchmark
della tappa 6: ~50 ms contro ~50 ns).

### Sviluppo e test incrementali

```bash
# Passo A: deve compilare. Il test arriva con il passo B.
go build ./internal/auth

# Passo B: formato, 32 byte, hash coerente, nessun duplicato su 100 token.
make test-acceptance RUN='^TestNewToken'

# Sblocca anche i due test del repository che usano NewToken.
make test-acceptance RUN='TestSessionRepoExpiryAndRevocation|TestSessionsAreDeletedWithTheirUser'
```

Errori frequenti:

- `return sum` → non compila: `cannot use sum (variable of type [32]byte) as []byte value in
  return statement`. `Sum256` restituisce un array; serve la slice `sum[:]`;
- `token := base64...` → non compila: `no new variables on left side of :=`. `token` è già
  dichiarato come risultato con nome: si assegna con `=`;
- import `"errors"` rimasto dopo aver tolto lo stub → `"errors" imported and not used`;
- `base64.StdEncoding` o `URLEncoding` → `token is not base64.RawURLEncoding` (padding `=` e,
  per `StdEncoding`, anche `+` e `/`);
- `HashToken(string(b))` (hash dei byte grezzi invece del token) → `hash is not SHA-256(token)`;
- `math/rand` al posto di `crypto/rand` → *nessun test lo scopre*: compila, i token sono unici, ma
  sono prevedibili. È un errore di sicurezza, non di funzionamento.

## Passo 2 — AUTH-2: login con errore generico (12')

**Dove entra nel servizio.** L'handler `login` (`auth_handlers.go`) decodifica il JSON e chiama
`Login`. Il risultato diventa 200 con il token; un errore passa da `respondErr`, che decide lo
status.

**Perché serve.** Il login è la porta d'ingresso, e un attaccante la interroga a ripetizione. Se le
risposte distinguessero "email inesistente" da "password errata", il login diventerebbe un modo per
scoprire chi è registrato (*user enumeration*). Lo stesso vale per i **tempi** di risposta.

**Come svilupparlo.** Tre passi in ordine, descritti nel TODO: A ricerca utente, B verifica
password, C creazione sessione. La sessione si crea **solo** alla fine, dopo una password corretta.

### Contratto di `Login`

| Caso | `Login` restituisce | Risposta HTTP | Sessione creata |
|---|---|---|---|
| email malformata (`not-an-email`) | `ErrInvalidCredentials` | 401 `invalid_credentials` | no |
| email sconosciuta | `ErrInvalidCredentials`, dopo bcrypt su `dummyHash` | 401 `invalid_credentials` | no |
| password errata o vuota | `ErrInvalidCredentials` | 401 `invalid_credentials` | no |
| `CredentialsByEmail` fallisce (DB giù) | `login: <errore>` | 500 `internal_error` | no |
| hash nel DB corrotto | `login: check password: ...` | 500 `internal_error` | no |
| `NewToken` o `CreateSession` falliscono | `login: <errore>` | 500 `internal_error` | no |
| credenziali corrette, email anche in maiuscolo | `LoginResult{Token, ExpiresAt}` | 200, `Cache-Control: no-store` | sì, con `SHA-256(token)` |

Le prime tre righe producono **lo stesso identico body**. Le ultime tre righe non sono colpa del
client: restano errori interni, avvolti con `%w`, e il dettaglio finisce solo nel log.

### `==` contro `errors.Is`

`ErrInvalidCredentials` è un **errore sentinella**: una variabile del package, confrontabile per
identità. Il test lo controlla con `err != auth.ErrInvalidCredentials`, cioè con `==`, apposta:
`fmt.Errorf("login: %w", ErrInvalidCredentials)` passerebbe `errors.Is`, ma porterebbe con sé
un messaggio diverso. Restituendo il valore esatto, nessun dettaglio può filtrare verso il client.

Gli errori interni invece si **avvolgono** con `%w`: aggiungono contesto (`login: ...`) e restano
riconoscibili con `errors.Is` nei layer superiori.

### Perché `dummyHash`

Misurato su questo servizio, con `curl -w '%{time_total}'`:

| Login con | Con `dummyHash` | Senza |
|---|---|---|
| email esistente, password errata | ~47 ms | ~47 ms |
| email inesistente | ~47 ms | **~1 ms** |

Senza il confronto fittizio la differenza si misura da remoto: un *timing side channel* che
rivela quali email sono registrate. *Nessun test lo scopre*: il TODO lo richiede esplicitamente.

### Sviluppo e test incrementali

```bash
# Verde già ora: deve restare verde.
make test-acceptance RUN=TestLoginStoreFailureIsNotACredentialError

# Passi A e B: i quattro rifiuti generici.
make test-acceptance RUN=TestLoginRejectsBadCredentialsGenerically
make test-acceptance RUN='TestLoginRejectsBadCredentialsGenerically/malformed_email'

# Passo C: sessione con hash, timestamp dall'orologio iniettato.
make test-acceptance RUN=TestLoginCreatesSessionWithHashOnly

# Tutti i test del login (compreso TestLogin di httpapi, già verde).
make test-acceptance RUN='^TestLogin'
```

Errori frequenti:

- `fmt.Errorf("login: %w", ErrInvalidCredentials)` → `want exactly ErrInvalidCredentials` nei casi
  `wrong_password` ed `empty_password`;
- restituire l'errore di validazione dell'email → fallisce `malformed_email`: sarebbe un 400 che
  rivela il motivo;
- `time.Now()` invece di `s.now()` → `ExpiresAt = ..., want ...` e, più avanti, i casi `expired` di
  `TestAuthenticate`: l'orologio finto è fermo al 1° ottobre 2026;
- errore di `CreateSession` tradotto in `ErrInvalidCredentials` → fallisce
  `TestLoginStoreFailureIsNotACredentialError`: un DB giù non è una password errata;
- sessione creata prima di `CheckPassword` → `1 sessions created after a failed login`;
- `dummyHash` dimenticato → *nessun test lo scopre*: vedi la tabella sopra.

## Passo 3 — AUTH-3: autenticazione e logout (8')

**Dove entra nel servizio.** `requireAuth` (AUTH-4) chiama `Authenticate` a ogni richiesta protetta;
l'handler `logout` chiama `Logout` dietro `requireAuth`, quindi con un token già verificato.

**Perché serve.** È qui che una stringa ricevuta in un header diventa un'identità (`Principal`:
ID, email, ruolo). Ed è qui che si decide se un fallimento è colpa del client (401) o del server
(500).

**Come svilupparlo.** `Authenticate` (passo A) traduce gli esiti dello store; `Logout` (passo B) è
una chiamata sola. Il test di `Authenticate` fa prima un `Login`: servono AUTH-1 e AUTH-2.

### Contratto di `Authenticate`

| Token | Esito dello store | `Authenticate` restituisce | Con `requireAuth` |
|---|---|---|---|
| `""` | non interrogato | `ErrUnauthenticated` | 401 |
| sconosciuto, scaduto o revocato | `ErrNotFound` | `ErrUnauthenticated` | 401 |
| valido | `Principal` | `Principal`, `nil` | l'handler riceve il principal |
| qualsiasi | altro errore (DB giù) | `authenticate: <errore>` | 500 |

**La scadenza la decide lo store**, non il tuo codice: la query in `postgres/sessions.go` (e il
fake) considera valida una sessione solo se `expires_at > now`. Tu passi `s.now().UTC()` e
traduci il risultato: per questo il caso `expired exactly at expires_at` di `TestAuthenticate` si
ottiene senza confrontare date.

**Perché 500 e non 401 con il DB giù.** Un 401 dice al client "la tua sessione non vale": l'app
disconnetterebbe l'utente, che non ha fatto nulla di sbagliato. Un 500 dice la verità: il problema
è nostro, riprova più tardi.

### Contratto di `Logout`

| Chiamata | Risultato |
|---|---|
| `Logout(token)` con sessione esistente | `nil`; dopo, `Authenticate(token)` → `ErrUnauthenticated` |
| secondo `Logout(token)` | ancora `nil`: è idempotente, `DeleteSession` non segnala righe mancanti |
| `DeleteSession` fallisce | `logout: <errore>` |

Via HTTP il secondo `DELETE /v1/auth/session` risponde invece 401: la sessione non esiste più e
`requireAuth` rifiuta la richiesta prima di arrivare a `Logout`.

### Sviluppo e test incrementali

```bash
# Verde già ora: deve restare verde.
make test-acceptance RUN=TestAuthenticateStoreFailureIsInternal

# Passo A: token valido, scaduto, vuoto, sconosciuto.
make test-acceptance RUN='^TestAuthenticate$'

# Passo B: revoca e idempotenza.
make test-acceptance RUN=TestLogoutRevokesSession

# Checkpoint: AUTH-1..3 contro PostgreSQL vero, poi restano solo i 3 test di AUTH-4.
make test-acceptance RUN=TestAuthServiceWithPostgres
make test-acceptance
```

Errori frequenti:

- ogni errore tradotto in `ErrUnauthenticated` → `want an internal error (500, not 401)`;
- token in chiaro passato allo store (`[]byte(token)`) → `valid token` e `valid just before expiry`
  falliscono: lo store conosce solo l'hash;
- `time.Now()` invece di `s.now()` → falliscono gli stessi due casi: l'ora vera è dopo la scadenza
  della sessione creata con l'orologio finto;
- `Logout` con il token in chiaro → `Authenticate after logout = <nil>`: la sessione non è stata
  cancellata.

## Passo 4 — AUTH-4: middleware `requireAuth` (10')

**Dove entra nel servizio.** `NewHandler` (`server.go`) avvolge con `requireAuth` solo
`GET /v1/me` e `DELETE /v1/auth/session`; register, login e health restano pubblici. Il middleware
gira prima dell'handler e, se tutto va bene, gli passa il principal nel context.

**Perché serve.** Gli handler non devono ripetere il controllo dell'header: lo fanno una volta qui.
Rispetto a `requestID` della tappa 1 cambia una cosa: `requireAuth` può **fermare** la richiesta
senza chiamare `next`.

**Come svilupparlo.** Prima `bearerToken` (passo A), che controlla solo il formato dell'header; poi
`requireAuth` (passo B), che chiama il service e sceglie fra quattro esiti.

### Contratto di `bearerToken`

| Header `Authorization` | `bearerToken` | Perché |
|---|---|---|
| assente | `""`, `false` | nessuna credenziale |
| `Basic YWRhOnB3` | `""`, `false` | schema diverso |
| `Bearer` o `Bearer ` | `""`, `false` | token vuoto |
| `Bearer <token>` o `bearer <token>` | `<token>`, `true` | lo schema non distingue le maiuscole (RFC 7235) |
| `Bearer   <token>  ` | `<token>`, `true` | gli spazi attorno al token si tolgono |
| `Bearer <oltre 128 caratteri>` | `""`, `false` | `maxTokenLen`: un header enorme non arriva al DB |

### Contratto di `requireAuth`

| `bearerToken` | `Authenticate` | Risposta | `next` chiamato |
|---|---|---|---|
| `false` | non chiamato | 401 `unauthenticated` + `WWW-Authenticate: Bearer realm="queuedesk"` | no |
| `true` | `ErrUnauthenticated` | 401 `unauthenticated` + `WWW-Authenticate` | no |
| `true` | altro errore | 500 `internal_error`, dettaglio nel log | no |
| `true` | `Principal` | quella dell'handler | sì, con `r.WithContext(withPrincipal(...))` |

Ogni ramo d'errore termina con `return`. 401 vuol dire "non so chi sei"; il 403 ("so chi sei, ma
non puoi") non si decide qui: spetta al service, che conosce le regole di dominio (tappa 4).

### Sviluppo e test incrementali

```bash
# Passi A e B: richieste senza credenziale valida.
make test-acceptance RUN='TestRequireAuth/(no_header|wrong_scheme|empty_bearer)'

# Passo B: token sconosciuto (401) e store guasto (500).
make test-acceptance RUN=TestRequireAuth

# Il principal arriva all'handler attraverso il context.
make test-acceptance RUN='^TestMe$'

# Logout via HTTP: riceve il token autenticato; senza header → 401.
make test-acceptance RUN='^TestLogout$'
```

Errori frequenti:

- ogni errore → 401 → fallisce `session store down is 500, not 401`;
- `return` dimenticato dopo `a.unauthorized(w)` → `body is not the error envelope: invalid
  character '{' after top-level value`: il body contiene due risposte attaccate;
- `r.WithContext(ctx)` chiamato ma risultato scartato, poi `next(w, r)` → `TestMe` riceve 500 e
  nel log c'è `me: principal missing from context`. `WithContext` non modifica `r`: restituisce una
  copia;
- `ctx` creato ma non usato → non compila: `declared and not used: ctx`;
- `scheme == "Bearer"` invece di `strings.EqualFold` → *nessun test lo scopre*: provalo con
  `curl -H "Authorization: bearer $TOKEN"`;
- `TrimSpace` o il limite `maxTokenLen` dimenticati → *nessun test lo scopre*: rileggi la tabella
  di `bearerToken`;
- import `"errors"` o `"strings"` mancanti → `undefined: errors`, `undefined: strings`.

## Evidenze dal vivo

Terminale 1: `make run`. Terminale 2:

```bash
B=http://localhost:8080
J='Content-Type: application/json'

curl -s -X POST $B/v1/auth/register -H "$J" -d '{"email":"ada@example.com","password":"correct-horse"}'
curl -si -X POST $B/v1/auth/login -H "$J" -d '{"email":"ada@example.com","password":"correct-horse"}'

TOKEN=$(curl -s -X POST $B/v1/auth/login -H "$J" \
  -d '{"email":"ada@example.com","password":"correct-horse"}' | sed -E 's/.*"token":"([^"]+)".*/\1/')
echo ${#TOKEN}                                         # 43

curl -s -H "Authorization: Bearer $TOKEN" $B/v1/me
curl -s -H "Authorization: bearer $TOKEN" $B/v1/me     # schema in minuscolo: stesso risultato
curl -si $B/v1/me                                      # senza header
curl -si -X DELETE -H "Authorization: Bearer $TOKEN" $B/v1/auth/session
curl -si -H "Authorization: Bearer $TOKEN" $B/v1/me    # stesso token, dopo il logout
```

Se ripeti la register con la stessa email ricevi 409 `email_taken`: va bene, passa al login.

| Prova | Risultato atteso |
|---|---|
| login corretto | 200, header `Cache-Control: no-store`, `token` di 43 caratteri, `token_type: "Bearer"`, `expires_at` = ora + `SESSION_TTL` |
| password errata, email inesistente, email malformata | **lo stesso identico body** 401 `invalid_credentials` |
| `curl -o /dev/null -s -w '%{time_total}\n'` su password errata ed email inesistente | tempi simili, ~50 ms entrambi |
| `GET /v1/me` con il token, anche con `bearer` minuscolo | 200 `"role":"requester"` |
| `GET /v1/me` senza header, o con `Bearer xyz` | 401 `unauthenticated` e header `WWW-Authenticate: Bearer realm="queuedesk"` |
| `DELETE /v1/auth/session`, poi `/v1/me` con lo stesso token | 204, poi 401 |
| secondo `DELETE /v1/auth/session` con lo stesso token | 401: la sessione non esiste più |
| register con `"role":"agent"` nel body | 201 ma con ruolo requester (fino alla tappa 6 il campo viene ignorato; poi 400) |
| login dell'agent demo, poi `/v1/me` | `"role":"agent"` |

Nel DB (`make db-psql`):

```sql
SELECT octet_length(token_hash), expires_at - created_at FROM sessions;
```

restituisce righe `32 | 1 day` (con `SESSION_TTL=24h`; PostgreSQL mostra 24 ore come `1 day`).
Nessuna colonna contiene il token in chiaro. Nel terminale di `make run` non compaiono password,
token né hash: cerca il valore di `$TOKEN` nei log, non deve esserci.

<details><summary>Aiuto — livello 1</summary>

- AUTH-1: il token deve essere imprevedibile. Quale package di `crypto` genera byte casuali?
- AUTH-2: il login non deve mai far capire *perché* fallisce, né con il testo né con i tempi.
- AUTH-3: "sessione non trovata" vuol dire "non autenticato"; tutto il resto è un problema interno.
- AUTH-4: il middleware ha quattro esiti: 401, 401, 500, oppure prosegue con il principal nel
  context.
</details>

<details><summary>Aiuto — livello 2</summary>

- `crypto/rand.Read`, `base64.RawURLEncoding.EncodeToString`, `sha256.Sum256` (restituisce un
  array: `sum[:]`).
- Login: normalizza l'email → `CredentialsByEmail` → (`ErrNotFound`: `CheckPassword(dummyHash, ...)`)
  → `CheckPassword` → `NewToken` → `CreateSession`.
- `ErrInvalidCredentials` va restituito **così com'è**: il test lo confronta con `==`.
- Il tempo viene da `s.now().UTC()`: i test usano un orologio finto.
- `Authenticate`: `PrincipalBySession(ctx, HashToken(token), s.now().UTC())`, poi uno `switch` con
  `errors.Is(err, ErrNotFound)`.
- AUTH-4: `strings.Cut`, `strings.EqualFold`, `withPrincipal` (già pronto),
  `next(w, r.WithContext(ctx))`.
</details>

<details><summary>Aiuto — livello 3 (AUTH-2, passo A)</summary>

```go
creds, err := s.users.CredentialsByEmail(ctx, email)
switch {
case errors.Is(err, ErrNotFound):
    _ = CheckPassword(dummyHash, password)
    return LoginResult{}, ErrInvalidCredentials
case err != nil:
    return LoginResult{}, fmt.Errorf("login: %w", err)
}
// passo B: CheckPassword → passo C: NewToken → Session{...} → CreateSession → LoginResult
```
</details>

<details><summary>Aiuto — livello 3 (AUTH-4, passo B)</summary>

```go
p, err := a.auth.Authenticate(r.Context(), token)
switch {
case errors.Is(err, auth.ErrUnauthenticated):
    a.unauthorized(w)
    return
case err != nil:
    a.serverError(w, r, err)
    return
}
next(w, r.WithContext(withPrincipal(r.Context(), p)))
```
</details>

## Fine tappa

Prima del commit verifica questa checklist:

- **AUTH-1**: token da `crypto/rand`, 43 caratteri `RawURLEncoding`; nel database solo
  `SHA-256(token)`;
- **AUTH-2**: email malformata, email inesistente e password errata danno lo stesso errore esatto,
  con tempi simili; i guasti interni restano 500; nessuna sessione dopo un login fallito;
- **AUTH-3**: token vuoto, sconosciuto, scaduto o revocato → 401; DB giù → 500; il logout è
  idempotente;
- **AUTH-4**: ogni ramo d'errore scrive una sola risposta e fa `return`; `bearer` minuscolo è
  accettato; l'handler riceve il principal dal context;
- `make test`, `make test-race`, `make test-acceptance` e le prove della tabella **Evidenze dal
  vivo** hanno l'esito atteso.

`make test-acceptance` verde, poi `git commit -am "tappa 2"` e passa alla tappa 3 con
`git switch -c lavoro/tappa-3 origin/tappa-3-ticket`.
