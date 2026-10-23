// Package httpapi espone QueueDesk via HTTP/JSON con net/http e il
// ServeMux con pattern "METODO /percorso/{param}" (Go 1.22+).
//
// Il package è diviso in più file (server.go, health.go, middleware.go,
// json.go, errors.go...): per Go sono un unico package, e ogni file vede
// tipi e funzioni degli altri senza import.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"queuedesk/internal/auth"
	"queuedesk/internal/ticket"
)

// AuthService è ciò che gli handler richiedono ad auth.Service.
// Le interfacce sono dichiarate qui, dal chiamante, e restano piccole.
type AuthService interface {
	Register(ctx context.Context, email, password string) (auth.User, error)
	Login(ctx context.Context, email, password string) (auth.LoginResult, error)
	Authenticate(ctx context.Context, token string) (auth.Principal, error)
	Logout(ctx context.Context, token string) error
}

// TicketService è ciò che gli handler richiedono a ticket.Service.
type TicketService interface {
	Create(ctx context.Context, actor ticket.Actor, title, description string) (ticket.Ticket, error)
	List(ctx context.Context, actor ticket.Actor, q ticket.ListQuery) ([]ticket.Ticket, error)
	Get(ctx context.Context, actor ticket.Actor, id int64) (ticket.Ticket, error)
	Events(ctx context.Context, actor ticket.Actor, id int64) ([]ticket.Event, error)
}

// Pinger verifica la raggiungibilità del database (*pgxpool.Pool lo implementa).
//
// Go: un'interfaccia elenca solo metodi. Qualunque tipo abbia un metodo
// Ping(context.Context) error la soddisfa automaticamente, senza scriverlo
// da nessuna parte. L'interfaccia la dichiara chi la usa (questo package),
// e contiene solo ciò che gli serve: un metodo.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps raccoglie le dipendenze degli handler. main la riempie con quelle
// vere, i test con dei fake (vedi helpers_test.go).
type Deps struct {
	Auth    AuthService
	Tickets TicketService
	DB      Pinger
	Logger  *slog.Logger
}

// api è il ricevitore di tutti gli handler: ogni metodo (a *api) vede le
// dipendenze come campi. È privato: fuori dal package si usa NewHandler.
type api struct {
	auth    AuthService
	tickets TicketService
	db      Pinger
	log     *slog.Logger
}

// NewHandler registra le route e applica i middleware.
// Ordine di esecuzione: requestID → log → recover → mux → (requireAuth) → handler.
func NewHandler(d Deps) http.Handler {
	// Un logger nil manderebbe in panic al primo log: in quel caso usiamo
	// un logger che scarta tutto.
	log := d.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	a := &api{auth: d.Auth, tickets: d.Tickets, db: d.DB, log: log}

	// Go: il ServeMux sceglie l'handler in base a metodo e percorso.
	// "GET /healthz" accetta solo GET: per gli altri metodi risponde da solo
	// 405 Method Not Allowed. a.healthz senza parentesi è un "method value":
	// la funzione healthz già legata ad a, passata come valore.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)

	mux.HandleFunc("POST /v1/auth/register", a.register)
	mux.HandleFunc("POST /v1/auth/login", a.login)
	mux.Handle("DELETE /v1/auth/session", a.requireAuth(a.logout))
	mux.Handle("GET /v1/me", a.requireAuth(a.me))

	mux.Handle("POST /v1/tickets", a.requireAuth(a.createTicket))
	mux.Handle("GET /v1/tickets", a.requireAuth(a.listTickets))
	mux.Handle("GET /v1/tickets/{id}", a.requireAuth(a.getTicket))
	mux.Handle("GET /v1/tickets/{id}/events", a.requireAuth(a.ticketEvents))

	// Go: *http.ServeMux soddisfa l'interfaccia http.Handler (ha il metodo
	// ServeHTTP). Ogni middleware avvolge il precedente: l'ultimo applicato
	// (requestID) è il primo a vedere la richiesta. logRequests sta fuori da
	// recoverPanics: così scrive la riga di log anche per una richiesta
	// andata in panic, con lo status 500 impostato da recoverPanics.
	var h http.Handler = mux
	h = a.recoverPanics(h)
	h = a.logRequests(h)
	h = requestID(h)
	return h
}

// NewServer configura i timeout: senza, un client lento può tenere aperte
// connessioni all'infinito (Slowloris).
//
// Go: &http.Server{...} crea la struct e ne restituisce l'indirizzo, cioè
// un *http.Server. I campi non indicati restano al valore zero.
func NewServer(addr string, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10, // 16 KiB: 16 spostato a sinistra di 10 bit = 16*1024
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}
