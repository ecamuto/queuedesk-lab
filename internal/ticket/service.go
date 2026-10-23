package ticket

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Store è la persistenza richiesta dal service; internal/postgres la implementa.
// Ogni metodo che modifica dati scrive anche l'evento di audit nella stessa
// transazione.
//
// Go: i commenti sui metodi di un'interfaccia sono il contratto: dicono
// quali errori di dominio l'implementazione deve restituire. Il fake dei
// test (fakes_test.go) e postgres.TicketRepo devono rispettarlo entrambi.
type Store interface {
	CreateTicket(ctx context.Context, t NewTicket) (Ticket, error)
	ListTickets(ctx context.Context, f ListFilter) ([]Ticket, error)
	// TicketByID restituisce ErrNotFound se il ticket non esiste.
	TicketByID(ctx context.Context, id int64) (Ticket, error)
	ListEvents(ctx context.Context, ticketID int64) ([]Event, error)
}

// Service implementa i casi d'uso dei ticket.
type Service struct {
	store Store
	log   *slog.Logger
}

// NewService crea il service.
//
// Go: un logger nil manderebbe in panic al primo log: in quel caso usiamo
// un logger che scarta tutto (stesso schema di httpapi.NewHandler).
func NewService(store Store, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: store, log: log}
}

// Create apre un ticket a nome di actor. Qualsiasi utente autenticato può
// aprire ticket.
func (s *Service) Create(ctx context.Context, actor Actor, title, description string) (Ticket, error) {
	// TODO(TICKET-1) — Validare e salvare un nuovo ticket.
	//
	// Dove serve: POST /v1/tickets (createTicket in ticket_handlers.go) passa
	// qui l'Actor ricavato dal principal e i campi grezzi del body JSON.
	// respondErr traduce ErrInvalidInput in 400 invalid_input e usa err.Error()
	// come messaggio: scrivilo leggibile, arriva al client.
	//
	// Passo A — Normalizzare e validare, PRIMA di chiamare lo store:
	//   1. title = strings.TrimSpace(title), lo stesso per description:
	//      "   " diventa "" e conta come titolo vuoto;
	//   2. conta i caratteri con utf8.RuneCountInString, non con len: len
	//      conta i byte, e "é" sono 2 byte ma 1 carattere (il CHECK SQL usa
	//      char_length, cioè caratteri);
	//   3. titolo con 0 o più di MaxTitleLen caratteri →
	//      fmt.Errorf("%w: title must be between 1 and %d characters",
	//      ErrInvalidInput, MaxTitleLen);
	//   4. description con più di MaxDescriptionLen caratteri (vuota va
	//      bene) → fmt.Errorf("%w: description must be at most %d characters",
	//      ErrInvalidInput, MaxDescriptionLen);
	//   5. con un input non valido restituisci subito l'errore: il test
	//      controlla che lo store non venga chiamato.
	//
	// Passo B — Salvare:
	//   1. s.store.CreateTicket(ctx, NewTicket{...}) con title e description
	//      già normalizzati e ReporterID: actor.UserID. Il reporter viene dal
	//      principal autenticato, mai dal body;
	//   2. errore → fmt.Errorf("create ticket: %w", err): %w e non %v, così
	//      errors.Is ritrova ancora la causa;
	//   3. restituisci il Ticket dello store: ID, stato "open" e timestamp li
	//      assegna il database.
	//
	// Aggiungi gli import "strings" e "unicode/utf8". Rimuovi lo stub.
	// Fatto quando: make test-acceptance RUN='TestCreateValidation|TestStoreErrorsAreWrapped'.
	return Ticket{}, fmt.Errorf("TODO(TICKET-1) Create: %w", errors.ErrUnsupported)
}

// List restituisce i ticket visibili ad actor: tutti per un agent, solo i
// propri per un requester.
func (s *Service) List(ctx context.Context, actor Actor, q ListQuery) ([]Ticket, error) {
	// TODO(TICKET-2) — Tradurre la query string in un filtro sicuro.
	//
	// Dove serve: GET /v1/tickets?status=...&limit=... (listTickets). q
	// contiene le stringhe grezze della query string; lo store riceve invece
	// un ListFilter con valori tipizzati e già validati.
	//
	// Passo A — Costruire il filtro, partendo da f := ListFilter{Limit:
	// DefaultListLimit}:
	//   1. q.Status == "" → nessun filtro di stato. Altrimenti
	//      st, err := ParseStatus(q.Status): l'errore (ErrInvalidInput con
	//      messaggio già pronto) si restituisce così com'è; se ok, f.Status = st;
	//   2. q.Limit == "" → resta DefaultListLimit (20). Altrimenti
	//      n, err := strconv.Atoi(q.Limit); conversione fallita, n < 1 oppure
	//      n > MaxListLimit → un solo messaggio per i tre casi:
	//      fmt.Errorf("%w: limit must be an integer between 1 and %d",
	//      ErrInvalidInput, MaxListLimit); se ok, f.Limit = n;
	//   3. !actor.IsAgent → f.ReporterID = actor.UserID. Lo scope lo impone il
	//      server: il client non ha modo di chiedere i ticket altrui. Per un
	//      agent ReporterID resta vuoto, cioè "tutti".
	//
	// Passo B — Interrogare lo store, solo con un filtro valido:
	//   1. ts, err := s.store.ListTickets(ctx, f); errore →
	//      fmt.Errorf("list tickets: %w", err);
	//   2. restituisci ts così com'è, anche vuota: l'handler risponde
	//      comunque "tickets": [] e non null.
	//
	// Aggiungi l'import "strconv". Rimuovi lo stub.
	// Fatto quando: make test-acceptance RUN=TestListFilter (il test
	// confronta, campo per campo, il filtro ricevuto dal fake).
	return nil, fmt.Errorf("TODO(TICKET-2) List: %w", errors.ErrUnsupported)
}

// Get restituisce un ticket se actor può vederlo, altrimenti ErrNotFound.
func (s *Service) Get(ctx context.Context, actor Actor, id int64) (Ticket, error) {
	// TODO(TICKET-3, passo A) — Applicare la policy di visibilità.
	//
	// Dove serve: GET /v1/tickets/{id} (getTicket) e, tramite Events, anche
	// GET /v1/tickets/{id}/events. La policy è nel commento del package, in
	// ticket.go.
	//
	//   1. t, err := s.store.TicketByID(ctx, id); errore →
	//      fmt.Errorf("get ticket %d: %w", id, err). Con %w l'ErrNotFound
	//      dello store resta riconoscibile e diventa 404;
	//   2. il ticket è visibile se actor.IsAgent oppure
	//      t.ReporterID == actor.UserID;
	//   3. non visibile → fmt.Errorf("get ticket %d: %w", id, ErrNotFound).
	//      ErrNotFound (404), NON un errore "forbidden": per un requester il
	//      ticket di un altro non esiste, così provando gli ID non scopre
	//      quali sono validi;
	//   4. visibile → return t, nil.
	//
	// Rimuovi lo stub.
	// Fatto quando: make test-acceptance RUN='TestGetVisibility/missing_ticket'
	// (gli altri casi controllano anche Events: servono il passo B).
	return Ticket{}, fmt.Errorf("TODO(TICKET-3) Get: %w", errors.ErrUnsupported)
}

// Events restituisce l'audit di un ticket con la stessa policy di Get.
func (s *Service) Events(ctx context.Context, actor Actor, id int64) ([]Event, error) {
	// TODO(TICKET-3, passo B) — Audit con la stessa policy di Get.
	//
	// Dove serve: GET /v1/tickets/{id}/events (ticketEvents).
	//
	//   1. if _, err := s.Get(ctx, actor, id); err != nil → restituisci err
	//      così com'è: è già avvolto ("get ticket %d: ...") e porta ErrNotFound
	//      per i ticket altrui. Riusare Get evita di riscrivere la policy;
	//   2. evs, err := s.store.ListEvents(ctx, id); errore →
	//      fmt.Errorf("list events of ticket %d: %w", id, err);
	//   3. return evs, nil.
	//
	// Senza il punto 1 un requester leggerebbe l'audit dei ticket altrui.
	// Rimuovi lo stub. Quando non resta più nessuno stub, togli anche
	// l'import "errors": non serve più.
	// Fatto quando: make test-acceptance RUN=TestGetVisibility.
	return nil, fmt.Errorf("TODO(TICKET-3) Events: %w", errors.ErrUnsupported)
}
