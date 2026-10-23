// Package ticket contiene il modello dei ticket e le regole applicative:
// validazione, visibilità, permessi e transizioni di stato.
// Non conosce né HTTP né SQL.
//
// Policy di visibilità (documentata anche nel README):
//   - un agent vede tutti i ticket e i relativi eventi;
//   - un requester vede solo i ticket che ha aperto; per un ticket altrui
//     riceve ErrNotFound (404), non ErrForbidden, così non scopre quali ID
//     esistono;
//   - claim e cambio di stato richiedono il ruolo agent (ErrForbidden, 403);
//     il cambio di stato è riservato all'agent assegnato (ErrNotAssignee, 403).
//
// Go: le dipendenze vanno in una sola direzione: httpapi e postgres
// importano ticket, ticket non importa nessuno dei due (solo notify, per il
// tipo Notification). Go vieta i cicli di import: se ticket importasse
// httpapi, che importa ticket, il codice non compilerebbe.
package ticket

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Status è lo stato di un ticket.
//
// Go: come auth.Role, un tipo string con nome: una funzione che riceve uno
// Status dichiara nella firma che si aspetta uno stato, non un testo qualsiasi.
type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusResolved   Status = "resolved"
)

// ParseStatus converte l'input del client in uno Status valido.
//
// Go: Status(s) è una conversione di tipo (string → Status), sempre lecita
// fra tipi con la stessa base. Non controlla il valore: per questo serve lo
// switch. "switch st := ...; st {" dichiara st visibile solo nello switch, e
// un case può elencare più valori.
func ParseStatus(s string) (Status, error) {
	switch st := Status(s); st {
	case StatusOpen, StatusInProgress, StatusResolved:
		return st, nil
	}
	return "", fmt.Errorf("%w: status must be one of open, in_progress, resolved", ErrInvalidInput)
}

// Ticket è una richiesta di supporto.
//
// Go: AssigneeID è un *string perché un puntatore può essere nil, una string
// no (il suo valore zero è ""). Così si distingue "nessun assegnatario" (nil,
// NULL in SQL) da un valore vero; pgx scrive nil quando la colonna è NULL.
type Ticket struct {
	ID          int64 // Go: int64 come il BIGINT di PostgreSQL
	Title       string
	Description string
	Status      Status
	ReporterID  string
	AssigneeID  *string // nil finché nessun agent ha fatto claim
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// EventKind è il tipo di evento di audit.
type EventKind string

const (
	EventCreated       EventKind = "created"
	EventClaimed       EventKind = "claimed"
	EventStatusChanged EventKind = "status_changed"
)

// Event è una riga dell'audit trail di un ticket.
//
// Go: json.RawMessage è un []byte che contiene JSON già pronto. Payload si
// legge così com'è dalla colonna JSONB e si rimanda al client senza
// decodificarlo: il package non deve conoscere la forma di ogni payload.
type Event struct {
	ID        int64
	TicketID  int64
	ActorID   string
	Kind      EventKind
	Payload   json.RawMessage
	CreatedAt time.Time
}

// Actor è chi esegue l'operazione. Il layer HTTP lo ricava dal principal
// autenticato: il package ticket non dipende da auth.
type Actor struct {
	UserID  string
	IsAgent bool
}

// NewTicket sono i dati validati per creare un ticket.
type NewTicket struct {
	Title       string
	Description string
	ReporterID  string
}

// ListQuery è l'input grezzo del client per la lista (query string).
//
// Go: tutto string, non int: la conversione e i suoi errori sono regole del
// service (List), non del layer HTTP.
type ListQuery struct {
	Status string // "" = tutti
	Limit  string // "" = DefaultListLimit
}

// ListFilter è il filtro validato passato allo store.
type ListFilter struct {
	ReporterID string // "" = tutti i reporter (solo agent)
	Status     Status // "" = tutti gli stati
	Limit      int
}

// Limiti di validazione.
const (
	MaxTitleLen       = 200  // caratteri (rune), coerente con il CHECK SQL
	MaxDescriptionLen = 5000 // caratteri (rune)
	DefaultListLimit  = 20
	MaxListLimit      = 100
)

// Errori di dominio. Il layer HTTP li traduce in status code.
//
// Go: auth ha anche lui un ErrInvalidInput: sono due variabili distinte
// (auth.ErrInvalidInput, ticket.ErrInvalidInput), il nome del package le
// separa e errors.Is non le confonde.
var (
	ErrInvalidInput = errors.New("invalid input")
	ErrNotFound     = errors.New("ticket not found")
)
