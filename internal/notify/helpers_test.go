package notify_test

import "log/slog"

// discardLogger scarta i log: i test restano silenziosi.
// Go: slog.DiscardHandler (Go 1.24+) è un handler che non scrive nulla.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }
