package ticket_test

import (
	"sync"

	"queuedesk/internal/notify"
	"queuedesk/internal/ticket"
)

// fakePublisher registra le notifiche e può simulare una coda piena.
type fakePublisher struct {
	mu   sync.Mutex
	sent []notify.Notification
	err  error
}

// Go: *fakePublisher soddisfa ticket.Publisher perché ha TryPublish con
// la stessa firma: il service non sa che non è una notify.Queue.
func (p *fakePublisher) TryPublish(n notify.Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.sent = append(p.sent, n)
	return nil
}

func (p *fakePublisher) kinds() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, n := range p.sent {
		out = append(out, n.Kind)
	}
	return out
}

// newServiceWithPublisher crea il service con un publisher finto.
func newServiceWithPublisher() (*ticket.Service, *fakeStore, *fakePublisher) {
	st, pub := newFakeStore(), &fakePublisher{}
	return ticket.NewService(st, pub, nil), st, pub
}
