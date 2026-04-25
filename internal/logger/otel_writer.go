package logger

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/log"
)

// otelLogWriter is a zerolog-compatible io.Writer that parses each
// JSON record zerolog emits and re-issues it as an OpenTelemetry
// log.Record with full attributes preserved.
//
// # Background — why this exists
//
// The official otelzerolog bridge (Hook) cannot read fields off a
// zerolog.Event because zerolog's Hook interface predates and does not
// expose field retrieval (rs/zerolog issue #493 — open as of this
// writing). The bridge's docstring acknowledges the gap:
//
//	NOTE: Fields are not transformed because of
//	https://github.com/rs/zerolog/issues/493.
//
// Result: every clawker log line in Loki/Grafana arrives with the body
// + severity intact but ALL structured fields (`kind`, `id`, `action`,
// `container_id`, ...) silently dropped, making "access granted" /
// "event dispatched" lines useless for debugging.
//
// This writer routes around the limitation by hooking the layer
// BELOW zerolog's hook system: the actual JSON output. zerolog
// already serializes the entire event (level, message, time, fields)
// to a single JSON object per Write call; we decode that object,
// promote `level` → severity, `message`/`msg` → body, `time` →
// timestamp, and forward every remaining key as an OTEL attribute.
//
// The cost is one JSON round-trip per emitted log record. For
// in-process clawker volume this is well below noise.
type otelLogWriter struct {
	logger log.Logger

	// pool reuses parsed maps across writes so steady-state logging
	// allocates only the JSON decoder's internal buffers.
	pool sync.Pool
}

func newOtelLogWriter(logger log.Logger) *otelLogWriter {
	return &otelLogWriter{
		logger: logger,
		pool: sync.Pool{
			New: func() any { return make(map[string]any, 16) },
		},
	}
}

// Write implements io.Writer. zerolog calls Write once per record with
// a complete JSON object. Returns len(p), nil so zerolog never sees a
// partial-write situation; an OTEL emit error gets swallowed because
// there is nowhere to report it from a writer (the OTEL SDK's own
// error handler is wired in newOtelProvider — see logger.go).
func (w *otelLogWriter) Write(p []byte) (int, error) {
	fields := w.pool.Get().(map[string]any)
	defer func() {
		clear(fields)
		w.pool.Put(fields)
	}()

	if err := json.Unmarshal(p, &fields); err != nil {
		// Malformed line — drop. zerolog produced bad JSON would be
		// the bug to chase, not a runtime concern here.
		return len(p), nil
	}

	rec := log.Record{}

	if levelStr, ok := fields[zerolog.LevelFieldName].(string); ok {
		if lvl, err := zerolog.ParseLevel(levelStr); err == nil {
			rec.SetSeverity(zerologLevelToOTEL(lvl))
			rec.SetSeverityText(lvl.String())
		}
		delete(fields, zerolog.LevelFieldName)
	}

	if msg, ok := fields[zerolog.MessageFieldName].(string); ok {
		rec.SetBody(log.StringValue(msg))
		delete(fields, zerolog.MessageFieldName)
	}

	if ts, ok := parseTimestamp(fields[zerolog.TimestampFieldName]); ok {
		rec.SetTimestamp(ts)
	}
	delete(fields, zerolog.TimestampFieldName)

	// Remaining keys become OTEL attributes. Type-preserving
	// conversion so a numeric `port=4319` lands as an Int64 attribute,
	// not the string "4319".
	for k, v := range fields {
		rec.AddAttributes(log.KeyValue{Key: k, Value: anyToOTELValue(v)})
	}

	w.logger.Emit(context.Background(), rec)
	return len(p), nil
}

// zerologLevelToOTEL maps zerolog levels to OTEL severities — same
// mapping the upstream bridge uses, copied to avoid pulling the
// upstream module just for this conversion.
func zerologLevelToOTEL(level zerolog.Level) log.Severity {
	switch level {
	case zerolog.DebugLevel:
		return log.SeverityDebug
	case zerolog.InfoLevel:
		return log.SeverityInfo
	case zerolog.WarnLevel:
		return log.SeverityWarn
	case zerolog.ErrorLevel:
		return log.SeverityError
	case zerolog.PanicLevel:
		return log.SeverityFatal1
	case zerolog.FatalLevel:
		return log.SeverityFatal2
	default:
		return log.SeverityUndefined
	}
}

// parseTimestamp accepts the JSON shapes zerolog emits for the time
// field: RFC3339 string by default, or a numeric epoch value when
// configured with TimeFieldFormat = "" (we don't, but tolerate it).
func parseTimestamp(v any) (time.Time, bool) {
	switch tv := v.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, tv); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, tv); err == nil {
			return t, true
		}
	case float64:
		// Unix seconds (zerolog default for numeric times).
		sec := int64(tv)
		nsec := int64((tv - float64(sec)) * 1e9)
		return time.Unix(sec, nsec), true
	}
	return time.Time{}, false
}

// anyToOTELValue maps a JSON-decoded Go value to a typed OTEL log
// value. Falls back to a string repr for shapes the OTEL value model
// doesn't carry directly (slices, nested maps, nil).
func anyToOTELValue(v any) log.Value {
	switch tv := v.(type) {
	case string:
		return log.StringValue(tv)
	case bool:
		return log.BoolValue(tv)
	case float64:
		// JSON numbers always decode to float64. Promote to int64 if
		// it's a clean integer so attribute consumers see the natural
		// numeric type.
		if tv == float64(int64(tv)) {
			return log.Int64Value(int64(tv))
		}
		return log.Float64Value(tv)
	case nil:
		return log.StringValue("")
	default:
		// Slices, maps, anything exotic — JSON-encode as a string so
		// the value still surfaces in Loki/Grafana even if it can't
		// be projected as a typed attribute.
		if b, err := json.Marshal(tv); err == nil {
			return log.StringValue(string(b))
		}
		return log.StringValue("")
	}
}
