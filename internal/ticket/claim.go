package ticket

import (
	"context"
	"errors"
	"fmt"
)

// Errori del claim e del cambio di stato.
//
// Go: un blocco var può stare in qualunque file del package: questi errori
// sono definiti qui, vicino a chi li usa, ma ticket.ErrForbidden si vede da
// tutto il package come se fosse in ticket.go.
var (
	ErrForbidden         = errors.New("agent role required")
	ErrNotAssignee       = errors.New("only the assigned agent can change the status")
	ErrAlreadyClaimed    = errors.New("ticket already claimed")
	ErrInvalidTransition = errors.New("status transition not allowed")
)

// CanTransition riporta se il passaggio from → to è ammesso:
//
//	open → in_progress → resolved
//	in_progress → open        (l'agent mette in attesa il ticket)
//	resolved → in_progress    (riapertura)
//
// Restare nello stesso stato non è una transizione.
//
// Nessuna transizione tocca l'assegnatario: un ticket rimesso in "open"
// resta dell'agent che ne ha fatto claim (in attesa, per esempio di una
// risposta del reporter). Compare ancora nella coda filtrata per status=open,
// ma un claim di un altro agent riceve ErrAlreadyClaimed. Il claim è l'unico
// modo di assegnare un ticket; il rilascio è la voce 9 di BACKLOG.md.
//
// Go: lo switch su un valore confronta from con ogni case. A differenza di
// C e Java non serve break: dopo un case l'esecuzione esce dallo switch. Un
// valore non previsto (from vuoto o sconosciuto) arriva al return false.
func CanTransition(from, to Status) bool {
	switch from {
	case StatusOpen:
		return to == StatusInProgress
	case StatusInProgress:
		return to == StatusResolved || to == StatusOpen
	case StatusResolved:
		return to == StatusInProgress
	}
	return false
}

// Claim assegna il ticket all'agent. In contesa vince un solo agent; gli
// altri ricevono ErrAlreadyClaimed.
//
// Go: il service controlla il ruolo, lo store garantisce l'atomicità. La
// concorrenza la risolve PostgreSQL (vedi postgres.ClaimTicket): nel codice
// Go non serve nessun mutex, perché le richieste concorrenti possono
// arrivare anche da più istanze del servizio.
func (s *Service) Claim(ctx context.Context, actor Actor, id int64) (Ticket, error) {
	if !actor.IsAgent {
		return Ticket{}, ErrForbidden // prima il ruolo: un requester non tocca lo store
	}
	t, err := s.store.ClaimTicket(ctx, id, actor.UserID)
	if err != nil {
		// %w: ErrAlreadyClaimed e ErrNotFound restano riconoscibili.
		return Ticket{}, fmt.Errorf("claim ticket %d: %w", id, err)
	}
	s.publish(ctx, EventClaimed, t, actor.UserID)
	return t, nil
}

// ChangeStatus cambia lo stato; consentito solo all'agent assegnato.
func (s *Service) ChangeStatus(ctx context.Context, actor Actor, id int64, status string) (Ticket, error) {
	if !actor.IsAgent {
		return Ticket{}, ErrForbidden
	}
	// Go: il valore del client arriva come string e diventa uno Status solo
	// dopo la validazione: dallo store in giù circolano solo stati validi.
	to, err := ParseStatus(status)
	if err != nil {
		return Ticket{}, err
	}
	t, err := s.store.UpdateStatus(ctx, id, actor.UserID, to)
	if err != nil {
		return Ticket{}, fmt.Errorf("change status of ticket %d: %w", id, err)
	}
	s.publish(ctx, EventStatusChanged, t, actor.UserID)
	return t, nil
}
