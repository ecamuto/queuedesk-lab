# Backlog — estensioni fuori dal percorso obbligatorio

Idee per chi finisce in anticipo o vuole proseguire dopo il corso. Nessuna è necessaria per gli
acceptance test. Sono ordinate per rapporto valore/sforzo.

| # | Estensione | Concetti Go/DB | Sforzo |
|---|---|---|---|
| 1 | **Pulizia delle sessioni scadute**: una goroutine con `time.Ticker` esegue `DELETE FROM sessions WHERE expires_at <= now()` e si ferma con il context dello shutdown (l'indice `sessions_expires_at_idx` c'è già). | ticker, context, shutdown | S |
| 2 | **Envelope JSON anche per 404/405** delle rotte inesistenti: un `http.Handler` che avvolge il mux e intercetta le risposte di default. | middleware, ResponseWriter | S |
| 3 | **Rate limiting del login** per IP o email (token bucket in memoria, mappa con mutex e scadenza). | sync, tempo, sicurezza | M |
| 4 | **Paginazione a cursore** su `GET /v1/tickets` (`?after=<created_at,id>`), stabile anche con inserimenti concorrenti. | SQL keyset, API design | M |
| 5 | **Outbox transazionale**: l'evento di notifica viene scritto in una tabella `outbox` nella stessa transazione e un dispatcher la consuma con `SELECT … FOR UPDATE SKIP LOCKED`. Elimina la perdita di notifiche al crash (at-least-once). | transazioni, worker, idempotenza | L |
| 6 | **Endpoint di profiling**: `net/http/pprof` su una porta admin separata e non esposta (per esempio `127.0.0.1:6060`). | pprof, sicurezza operativa | S |
| 7 | **Metriche** via `expvar` (contatori della coda, richieste per status). | expvar, osservabilità | S |
| 8 | **Logout da tutti i dispositivi** (`DELETE FROM sessions WHERE user_id = $1`) e cambio password che revoca le sessioni. | auth, transazioni | S |
| 9 | **Riassegnazione e rilascio** di un ticket (`POST /v1/tickets/{id}/release`) con relativo evento di audit. | dominio, concorrenza | M |
| 10 | **Concorrenza ottimistica sulle PATCH** con `ETag`/`If-Match` basato su `updated_at`. | HTTP semantics | M |
| 11 | **Specifica OpenAPI** scritta a mano e verificata da un test che confronta le rotte del mux. | contratti API | M |
| 12 | **Server-Sent Events** per ricevere le notifiche in tempo reale (al posto del `LogHandler`). | streaming HTTP, context | L |

Restano esplicitamente fuori dal laboratorio, come da specifica: frontend, JWT, OAuth, refresh token,
email reali, ORM, GraphQL, gRPC, broker di messaggi, microservizi, Dockerfile applicativo,
Kubernetes, CI.
