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
	// TODO(CLAIM-3, passo A) — La tabella delle transizioni.
	//
	// Dove serve: UpdateStatus in internal/postgres/claim.go (già scritto) la
	// chiama sotto lock, dopo aver letto lo stato corrente; la usa anche il
	// fake dei test. È una regola di dominio: sta qui, in un posto solo.
	//
	// Uno switch su from basta:
	//   - StatusOpen       → true solo se to == StatusInProgress;
	//   - StatusInProgress → true se to è StatusResolved oppure StatusOpen;
	//   - StatusResolved   → true solo se to == StatusInProgress;
	//   - qualsiasi altro from (vuoto, "bogus") → false.
	// from == to non è mai fra i casi veri: restare nello stesso stato non è
	// una transizione. In Go un case non prosegue nel successivo: niente break.
	// Il "return false" qui sotto resta come ultimo return, dopo lo switch.
	// Fatto quando: make test-acceptance RUN=TestCanTransition (prova tutte
	// le 9 coppie di stati, più uno stato sconosciuto).
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
	// TODO(CLAIM-2) — Claim nel service: prima il ruolo, poi lo store.
	//
	// Dove serve: POST /v1/tickets/{id}/claim (claimTicket in
	// claim_handlers.go) passa qui l'Actor ricavato dal principal.
	//
	//   1. !actor.IsAgent → return Ticket{}, ErrForbidden, SENZA chiamare lo
	//      store: un requester non deve generare query (il test lo verifica);
	//   2. t, err := s.store.ClaimTicket(ctx, id, actor.UserID);
	//   3. errore → fmt.Errorf("claim ticket %d: %w", id, err): con %w
	//      ErrAlreadyClaimed ed ErrNotFound restano riconoscibili con errors.Is;
	//   4. return t, nil.
	//
	// Non controllare in Go se il ticket è già assegnato (TicketByID e poi
	// if t.AssigneeID != nil): reintrodurresti la race che CLAIM-1 elimina.
	// Lo decide il database, in un'operazione sola. Rimuovi lo stub.
	// Fatto quando: make test-acceptance RUN='^TestClaim$'.
	return Ticket{}, fmt.Errorf("TODO(CLAIM-2) Claim: %w", errors.ErrUnsupported)
}

// ChangeStatus cambia lo stato; consentito solo all'agent assegnato.
func (s *Service) ChangeStatus(ctx context.Context, actor Actor, id int64, status string) (Ticket, error) {
	// TODO(CLAIM-3, passo B) — Cambio di stato nel service.
	//
	// Dove serve: PATCH /v1/tickets/{id}/status {"status": "..."}
	// (changeStatus in claim_handlers.go). status è la stringa grezza del body.
	//
	// Nell'ordine:
	//   1. !actor.IsAgent → ErrForbidden, senza chiamare lo store;
	//   2. to, err := ParseStatus(status); errore (ErrInvalidInput → 400)
	//      restituito così com'è. Non convertire con Status(status): "done"
	//      arriverebbe allo store e diventerebbe un 409 invece di un 400;
	//   3. t, err := s.store.UpdateStatus(ctx, id, actor.UserID, to); errore →
	//      fmt.Errorf("change status of ticket %d: %w", id, err);
	//   4. return t, nil.
	//
	// Assegnatario (ErrNotAssignee) e transizione (ErrInvalidTransition) NON
	// si controllano qui: li verifica lo store dopo SELECT ... FOR UPDATE, così
	// lo stato non può cambiare fra il controllo e l'UPDATE (leggi
	// UpdateStatus in internal/postgres/claim.go). Rimuovi lo stub; l'import
	// "errors" resta, serve a errors.New in cima al file.
	// Fatto quando: make test-acceptance RUN=TestChangeStatus (richiede il
	// passo A: il fake chiama CanTransition).
	return Ticket{}, fmt.Errorf("TODO(CLAIM-3) ChangeStatus: %w", errors.ErrUnsupported)
}
