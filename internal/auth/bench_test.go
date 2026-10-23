package auth_test

import (
	"testing"

	"queuedesk/internal/auth"
)

// Confronto di costo usato nel blocco "test/robustezza":
//
//	go test -run '^$' -bench . -benchmem ./internal/auth
//
// bcrypt è lento apposta (decine di ms): rende costoso il brute force delle
// password. SHA-256 sul token costa decine di ns, e va bene così perché il
// token ha 256 bit di entropia.
//
// Go: un benchmark è una funzione BenchmarkXxx(b *testing.B). "for b.Loop()"
// (Go 1.24+) ripete il corpo finché la misura è stabile e non conta il tempo
// del codice prima del ciclo. -benchmem aggiunge byte e allocazioni per
// operazione; -run '^$' salta i test normali (nessun nome corrisponde).
func BenchmarkHashPassword(b *testing.B) {
	for b.Loop() {
		if _, err := auth.HashPassword("correct-horse-battery"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHashToken(b *testing.B) {
	token := "q1w2e3r4t5y6u7i8o9p0a1s2d3f4g5h6j7k8l9z0x1c"
	for b.Loop() {
		auth.HashToken(token)
	}
}
