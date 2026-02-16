package iostreams

import "github.com/rs/zerolog"

// Logger provides diagnostic file logging for the command layer.
// *zerolog.Logger satisfies this interface directly — no adapter needed.
// Production always has a real logger (factory sets it via &logger.Log).
// Tests use loggertest.New() or loggertest.NewNop() when needed.
type Logger interface {
	Debug() *zerolog.Event
	Info() *zerolog.Event
	Warn() *zerolog.Event
	Error() *zerolog.Event
}
