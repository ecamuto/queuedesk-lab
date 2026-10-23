package ticket

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"
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
