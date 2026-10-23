// Package config legge e valida la configurazione di QueueDesk dalle
// variabili d'ambiente. Tutta la validazione avviene all'avvio: se qualcosa
// non va il processo termina subito, elencando ogni errore in un colpo solo.
//
// Go: il commento subito sopra "package" è la documentazione del package
// (la mostra `go doc ./internal/config`). Ogni cartella è un package; il
// nome con cui lo si importa è il percorso: "queuedesk/internal/config".
// "internal" ha un significato speciale: solo il codice di questo modulo
// può importarlo.
package config

// Go: gli import standard e quelli del modulo stanno in un unico blocco.
// Un import non usato è un errore di compilazione, non un warning.
import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"time"
)

// Config contiene i parametri runtime del servizio.
//
// Go: una struct è un insieme di campi con nome e tipo. I campi che iniziano
// con la maiuscola sono esportati (visibili da altri package), quelli con la
// minuscola no: in Go la visibilità si decide così, senza public/private.
type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	SessionTTL      time.Duration // Go: time.Duration è un int64 di nanosecondi
	ShutdownTimeout time.Duration
	NotifyWorkers   int
	NotifyQueueSize int
}

// Valori di default usati quando la variabile è assente o vuota.
//
// Go: un blocco const raggruppa più costanti. 24 * time.Hour è calcolato in
// fase di compilazione.
const (
	DefaultHTTPAddr        = ":8080"
	DefaultSessionTTL      = 24 * time.Hour
	DefaultShutdownTimeout = 10 * time.Second
	DefaultNotifyWorkers   = 4
	DefaultNotifyQueueSize = 64
)

// Limiti accettati: valori fuori intervallo sono quasi sempre un errore di
// battitura (es. SESSION_TTL=24 invece di 24h).
//
// Go: minuscole, quindi private al package. 10_000: il trattino basso
// separa le cifre e serve solo alla leggibilità.
const (
	minSessionTTL      = time.Minute
	maxSessionTTL      = 30 * 24 * time.Hour
	minShutdownTimeout = time.Second
	maxShutdownTimeout = 5 * time.Minute
	maxNotifyWorkers   = 64
	maxNotifyQueueSize = 10_000
)

// Load costruisce la Config leggendo le variabili tramite getenv.
// In produzione si passa os.Getenv; nei test una funzione su una mappa,
// senza toccare l'ambiente del processo.
//
// Go: getenv è un parametro di tipo funzione, "func(string) string". In Go
// le funzioni sono valori: si passano, si salvano in variabili e in campi.
// Load restituisce due valori: il risultato e un error. È la convenzione Go:
// niente eccezioni, l'errore è un valore che il chiamante deve controllare.
func Load(getenv func(string) string) (Config, error) {
	// Go: "p := parser{...}" dichiara e inizializza p in un colpo solo (:=).
	// Il tipo lo deduce il compilatore. parser{getenv: getenv} è un
	// "composite literal": i campi non nominati (errs) partono dal valore
	// zero, qui una slice nil, pronta per append.
	p := parser{getenv: getenv}

	// Ogni metodo di p legge una variabile; se qualcosa non va registra
	// l'errore in p.errs e va avanti, così li raccogliamo tutti.
	cfg := Config{
		DatabaseURL:     p.str("DATABASE_URL", ""),
		HTTPAddr:        p.str("HTTP_ADDR", DefaultHTTPAddr),
		SessionTTL:      p.duration("SESSION_TTL", DefaultSessionTTL, minSessionTTL, maxSessionTTL),
		ShutdownTimeout: p.duration("SHUTDOWN_TIMEOUT", DefaultShutdownTimeout, minShutdownTimeout, maxShutdownTimeout),
		NotifyWorkers:   p.integer("NOTIFY_WORKERS", DefaultNotifyWorkers, 1, maxNotifyWorkers),
		NotifyQueueSize: p.integer("NOTIFY_QUEUE_SIZE", DefaultNotifyQueueSize, 1, maxNotifyQueueSize),
	}
	p.checkDatabaseURL(cfg.DatabaseURL)
	p.checkAddr(cfg.HTTPAddr)

	// TODO(BASE-1, passo C) — Restituire tutti gli errori di configurazione.
	//
	// Situazione attuale: se più variabili sono errate, questo blocco restituisce
	// solo p.errs[0]. Chi avvia il servizio deve correggere un errore per volta.
	//
	// Risultato richiesto:
	//   - nessun errore  → restituisci cfg, nil;
	//   - uno o più errori → uniscili con errors.Join(p.errs...), aggiungi il
	//     prefisso "invalid configuration:" e restituisci Config{} più l'errore.
	//
	// Passi:
	//   1. aggiungi "errors" agli import;
	//   2. chiama errors.Join(p.errs...);
	//   3. se il risultato non è nil, avvolgilo con
	//      fmt.Errorf("invalid configuration:\n%w", err);
	//   4. sostituisci il blocco provvisorio qui sotto.
	//
	// Perché "p.errs...": ... espande la slice e passa ogni elemento come
	// argomento separato a errors.Join.
	// Fatto quando: TestLoadErrors mostra insieme DATABASE_URL e SESSION_TTL.
	if len(p.errs) > 0 {
		return Config{}, p.errs[0]
	}
	return cfg, nil
}

// LogValue implementa slog.LogValuer: la password contenuta in DATABASE_URL
// non finisce mai nei log.
//
// Go: "(c Config)" è il receiver, cioè questa è un metodo del tipo Config.
// Config non dichiara da nessuna parte di implementare slog.LogValuer: basta
// avere il metodo LogValue() slog.Value. Le interfacce Go sono implicite.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("database_url", redactURL(c.DatabaseURL)),
		slog.String("http_addr", c.HTTPAddr),
		slog.String("session_ttl", c.SessionTTL.String()),
		slog.String("shutdown_timeout", c.ShutdownTimeout.String()),
		slog.Int("notify_workers", c.NotifyWorkers),
		slog.Int("notify_queue_size", c.NotifyQueueSize),
	)
}

// redactURL sostituisce la password con "xxxxx".
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	return u.Redacted()
}

// parser accumula gli errori invece di fermarsi al primo.
//
// Go: tipo minuscolo, quindi privato: fuori dal package nessuno lo vede.
type parser struct {
	getenv func(string) string
	errs   []error // Go: []error è una slice, un array che cresce con append
}

// fail registra un errore formattato come fmt.Errorf.
//
// Go: "(p *parser)" è un receiver puntatore: il metodo modifica il parser
// originale. Con "(p parser)" lavorerebbe su una copia e l'append andrebbe
// perso. "args ...any" è variadico: accetta zero o più argomenti di
// qualunque tipo (any è un alias di interface{}).
func (p *parser) fail(format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf(format, args...))
}

// str restituisce la variabile key, oppure def se è vuota.
// È il modello per duration e integer.
func (p *parser) str(key, def string) string {
	if v := p.getenv(key); v != "" {
		return v
	}
	return def
}

// duration legge una durata Go ("30s", "15m", "24h") e controlla che stia
// in [lo, hi].
//
// Go: "def, lo, hi time.Duration": parametri consecutivi dello stesso tipo
// condividono la dichiarazione del tipo.
func (p *parser) duration(key string, def, lo, hi time.Duration) time.Duration {
	// TODO(BASE-1, passo A) — Leggere una durata opzionale dall'ambiente.
	//
	// Input: nome variabile, default e intervallo ammesso. Esempio:
	// duration("SESSION_TTL", 24*time.Hour, time.Minute, 30*24*time.Hour).
	//
	// Gestisci questi casi, in ordine:
	//   1. variabile vuota: restituisci def; non è un errore;
	//   2. valore non interpretabile, es. "24": registra con p.fail
	//      "%s: %q is not a duration (examples: 30s, 15m, 24h)" e restituisci 0;
	//   3. durata fuori da [lo, hi]: registra con p.fail
	//      "%s: %s is out of range [%s, %s]";
	//   4. restituisci sempre la durata convertita nei casi 3 e 4.
	//
	// Usa time.ParseDuration(raw). p.fail accumula l'errore: non termina Load.
	// Sostituisci il return provvisorio qui sotto.
	// Fatto quando: make test-acceptance RUN=TestLoadCustomValues.
	return def
}

// integer legge un intero e controlla che stia in [lo, hi].
func (p *parser) integer(key string, def, lo, hi int) int {
	// TODO(BASE-1, passo B) — Leggere un intero opzionale dall'ambiente.
	//
	// Non serve indovinare cosa significhi "come duration". I casi completi:
	//   1. variabile vuota: restituisci def;
	//   2. strconv.Atoi(raw) fallisce, es. "four": registra con p.fail
	//      "%s: %q is not an integer" e restituisci 0;
	//   3. valore fuori da [lo, hi]: registra con p.fail
	//      "%s: %d is out of range [%d, %d]";
	//   4. restituisci sempre l'intero convertito nei casi 3 e 4.
	//
	// Aggiungi "strconv" agli import e sostituisci il return provvisorio.
	// Fatto quando: make test-acceptance RUN=TestLoadCustomValues.
	return def
}

// checkDatabaseURL non include mai l'URL nei messaggi: può contenere la password.
func (p *parser) checkDatabaseURL(raw string) {
	if raw == "" {
		p.fail("DATABASE_URL: required (example: postgres://user:pass@localhost:5432/db?sslmode=disable)")
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		p.fail("DATABASE_URL: not a valid URL")
		return
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		p.fail("DATABASE_URL: scheme must be postgres:// or postgresql://")
	}
	if u.Host == "" {
		p.fail("DATABASE_URL: host is missing")
	}
}

// checkAddr verifica il formato host:porta (":8080" va bene: host vuoto
// significa "tutte le interfacce").
func (p *parser) checkAddr(addr string) {
	// Go: "_" scarta un valore restituito che non ci serve (qui l'host).
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		p.fail("HTTP_ADDR: %q must be host:port (example: :8080)", addr)
		return
	}
	// Go: net.LookupPort accetta un numero da 0 a 65535 o un nome di servizio
	// (":http" è la porta 80) e non usa la rete. Senza questo controllo ":abc"
	// e ":99999" passerebbero, e l'errore arriverebbe solo al listen, da solo
	// invece che insieme agli altri errori di configurazione.
	if _, err := net.LookupPort("tcp", port); err != nil {
		p.fail("HTTP_ADDR: %q has an invalid port (example: :8080)", addr)
	}
}
