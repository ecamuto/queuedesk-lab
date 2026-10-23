package ticket

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"queuedesk/internal/notify"
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
	// ClaimTicket assegna il ticket solo se non è già assegnato.
	// Errori: ErrNotFound, ErrAlreadyClaimed.
	ClaimTicket(ctx context.Context, id int64, agentID string) (Ticket, error)
	// UpdateStatus cambia stato solo se actorID è l'assegnatario e la
	// transizione è ammessa. Errori: ErrNotFound, ErrNotAssignee,
	// ErrInvalidTransition.
	UpdateStatus(ctx context.Context, id int64, actorID string, to Status) (Ticket, error)
	ListEvents(ctx context.Context, ticketID int64) ([]Event, error)
}

// Publisher accoda una notifica senza bloccare (implementato da notify.Queue).
//
// Go: un'interfaccia di un solo metodo. Il service non sa che dietro c'è un
// worker pool: nei test si passa un fake che registra le chiamate.
type Publisher interface {
	TryPublish(n notify.Notification) error
}

// Service implementa i casi d'uso dei ticket.
type Service struct {
	store Store
	pub   Publisher
	log   *slog.Logger
}

// NewService crea il service. pub può essere nil: nessuna notifica.
//
// Go: un valore di tipo interfaccia può essere nil. Va controllato prima di
// chiamarne un metodo (lo fa publish): chiamare un metodo su un'interfaccia
// nil è un panic.
func NewService(store Store, pub Publisher, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: store, pub: pub, log: log}
}

// Create apre un ticket a nome di actor. Qualsiasi utente autenticato può
// aprire ticket.
func (s *Service) Create(ctx context.Context, actor Actor, title, description string) (Ticket, error) {
	// Go: i parametri sono copie locali: riassegnarli non tocca i valori del
	// chiamante.
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)
	// Go: len conterebbe i byte; RuneCountInString conta i caratteri Unicode
	// (rune), come char_length nel CHECK SQL. "perché" sono 7 caratteri ma
	// 8 byte.
	if n := utf8.RuneCountInString(title); n == 0 || n > MaxTitleLen {
		return Ticket{}, fmt.Errorf("%w: title must be between 1 and %d characters", ErrInvalidInput, MaxTitleLen)
	}
	if utf8.RuneCountInString(description) > MaxDescriptionLen {
		return Ticket{}, fmt.Errorf("%w: description must be at most %d characters", ErrInvalidInput, MaxDescriptionLen)
	}
	// ReporterID viene dal principal autenticato, mai dal body della richiesta.
	nt := NewTicket{Title: title, Description: description, ReporterID: actor.UserID}
	t, err := s.store.CreateTicket(ctx, nt)
	if err != nil {
		return Ticket{}, fmt.Errorf("create ticket: %w", err)
	}
	// TODO(NOTIFY-4) — Notificare la creazione, solo dopo il successo.
	//
	// Dove serve: s.publish (in fondo a questo file, già scritto) costruisce
	// la notify.Notification per il reporter del ticket e chiama
	// s.pub.TryPublish; con pub == nil, come in newService dei test, non fa
	// nulla.
	//
	// Qui, dopo il controllo dell'errore dello store e prima del return:
	//   s.publish(ctx, EventCreated, t, actor.UserID)
	// Lo store ha già fatto commit: se avesse fallito non ci sarebbe nulla da
	// notificare. publish non restituisce errori: coda piena o chiusa
	// diventano un WARN nel log e la creazione resta riuscita (best effort).
	// Lo stesso schema va in Claim e ChangeStatus (claim.go).
	// Fatto quando: make test-acceptance RUN='TestPublishesAfterSuccess|TestNoPublishOnFailure|TestOperationsSucceedWhenQueueIsFull'
	// (gli ultimi due sono verdi già con lo stub: devono restarlo).
	return t, nil
}

// List restituisce i ticket visibili ad actor: tutti per un agent, solo i
// propri per un requester.
func (s *Service) List(ctx context.Context, actor Actor, q ListQuery) ([]Ticket, error) {
	f := ListFilter{Limit: DefaultListLimit}
	if q.Status != "" {
		st, err := ParseStatus(q.Status)
		if err != nil {
			return nil, err // Go: nil è il valore zero di una slice
		}
		f.Status = st
	}
	if q.Limit != "" {
		// Go: strconv.Atoi fallisce su "abc" e "1.5"; i limiti li
		// controlliamo noi. Un unico messaggio per entrambi i casi.
		n, err := strconv.Atoi(q.Limit)
		if err != nil || n < 1 || n > MaxListLimit {
			return nil, fmt.Errorf("%w: limit must be an integer between 1 and %d", ErrInvalidInput, MaxListLimit)
		}
		f.Limit = n
	}
	if !actor.IsAgent {
		f.ReporterID = actor.UserID // il filtro lo impone il server, non il client
	}
	ts, err := s.store.ListTickets(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	return ts, nil
}

// Get restituisce un ticket se actor può vederlo, altrimenti ErrNotFound.
func (s *Service) Get(ctx context.Context, actor Actor, id int64) (Ticket, error) {
	t, err := s.store.TicketByID(ctx, id)
	if err != nil {
		return Ticket{}, fmt.Errorf("get ticket %d: %w", id, err)
	}
	// Policy di visibilità: un agent vede tutto, un requester solo i propri.
	// Events passa da Get, quindi la regola sta solo qui.
	if !actor.IsAgent && t.ReporterID != actor.UserID {
		// Stesso errore di un ID inesistente: il requester non distingue i
		// due casi.
		return Ticket{}, fmt.Errorf("get ticket %d: %w", id, ErrNotFound)
	}
	return t, nil
}

// Events restituisce l'audit di un ticket con la stessa policy di Get.
func (s *Service) Events(ctx context.Context, actor Actor, id int64) ([]Event, error) {
	// Go: "_" scarta il ticket: qui Get serve solo per il controllo di
	// visibilità (ed eventualmente per il suo errore).
	if _, err := s.Get(ctx, actor, id); err != nil {
		return nil, err
	}
	evs, err := s.store.ListEvents(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list events of ticket %d: %w", id, err)
	}
	return evs, nil
}

// publish è best effort: viene chiamato dopo il commit e un fallimento
// (coda piena o chiusa) viene solo registrato. La transazione resta valida.
// Con un Publisher nil le notifiche sono disattivate (comodo nei test).
//
// Go: publish non restituisce un error di proposito. Un errore qui non deve
// trasformare in fallimento un'operazione già salvata nel database.
func (s *Service) publish(ctx context.Context, kind EventKind, t Ticket, actorID string) {
	if s.pub == nil {
		return
	}
	// Go: string(kind) converte il tipo con nome nella string sottostante:
	// notify non conosce i tipi di ticket.
	n := notify.Notification{
		Kind:        string(kind),
		TicketID:    t.ID,
		ActorID:     actorID,
		RecipientID: t.ReporterID,
		Status:      string(t.Status),
	}
	if err := s.pub.TryPublish(n); err != nil {
		s.log.WarnContext(ctx, "notification not queued", "kind", kind, "ticket_id", t.ID, "err", err)
	}
}
