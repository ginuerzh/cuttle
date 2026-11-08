// Package logging provides the cuttle logger: a log/slog-backed core logger
// whose level can be changed while the process is running.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-gost/core/logger"
)

const (
	levelTrace slog.Level = -8
	levelFatal slog.Level = 12
)

// Logger implements core/logger.Logger on top of log/slog. All children
// created with WithFields share the same LevelVar, so SetLevel affects the
// whole tree.
type Logger struct {
	base   *slog.Logger
	logger *slog.Logger
	always *slog.Logger
	level  *slog.LevelVar
	format string
	args   []any
}

// New creates a logger writing JSON or text to stderr.
func New(level, format string) (*Logger, error) {
	return newLogger(level, format, os.Stderr)
}

func newLogger(level, format string, out io.Writer) (*Logger, error) {
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}
	f, err := parseFormat(format)
	if err != nil {
		return nil, err
	}

	levelVar := new(slog.LevelVar)
	levelVar.Set(convertLevel(lvl))

	base := slog.New(makeHandler(out, f, levelVar))
	always := slog.New(makeHandler(out, f, levelTrace))
	return &Logger{
		base:   base,
		logger: base,
		always: always,
		level:  levelVar,
		format: string(f),
	}, nil
}

func makeHandler(out io.Writer, f logger.LogFormat, level slog.Leveler) slog.Handler {
	opts := &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	}
	if f == logger.TextFormat {
		return slog.NewTextHandler(out, opts)
	}
	return slog.NewJSONHandler(out, opts)
}

// SetDefault routes the global slog default through this logger so packages
// that use slog directly (llm-api-converter) share its handler and level.
func (l *Logger) SetDefault() {
	if l == nil || l.base == nil {
		return
	}
	slog.SetDefault(l.base)
}

// SetLevel changes the level of this logger and every WithFields child.
func (l *Logger) SetLevel(level string) error {
	lvl, err := parseLevel(level)
	if err != nil {
		return err
	}
	l.level.Set(convertLevel(lvl))
	return nil
}

// Format returns the output format selected at construction time.
func (l *Logger) Format() string {
	if l == nil {
		return ""
	}
	return l.format
}

// GetLevel returns the current level.
func (l *Logger) GetLevel() logger.LogLevel {
	if l == nil || l.level == nil {
		return logger.InfoLevel
	}
	return revertLevel(l.level.Level())
}

// IsLevelEnabled reports whether a message at level would be emitted.
func (l *Logger) IsLevelEnabled(level logger.LogLevel) bool {
	if l == nil || l.level == nil {
		return false
	}
	return l.level.Level() <= convertLevel(level)
}

// WithFields returns a child sharing the same level and handler.
func (l *Logger) WithFields(fields map[string]any) logger.Logger {
	args := mergeArgs(l.args, fields)
	return &Logger{
		base:   l.base,
		logger: l.base.With(args...),
		always: l.always,
		level:  l.level,
		format: l.format,
		args:   args,
	}
}

// Trace logs at trace level.
func (l *Logger) Trace(args ...any) { l.log(levelTrace, 3, args...) }

// Tracef logs a formatted message at trace level.
func (l *Logger) Tracef(format string, args ...any) { l.logf(levelTrace, format, args...) }

// Debug logs at debug level.
func (l *Logger) Debug(args ...any) { l.log(slog.LevelDebug, 3, args...) }

// Debugf logs a formatted message at debug level.
func (l *Logger) Debugf(format string, args ...any) { l.logf(slog.LevelDebug, format, args...) }

// Info logs at info level.
func (l *Logger) Info(args ...any) { l.log(slog.LevelInfo, 3, args...) }

// Infof logs a formatted message at info level.
func (l *Logger) Infof(format string, args ...any) { l.logf(slog.LevelInfo, format, args...) }

// Warn logs at warn level.
func (l *Logger) Warn(args ...any) { l.log(slog.LevelWarn, 3, args...) }

// Warnf logs a formatted message at warn level.
func (l *Logger) Warnf(format string, args ...any) { l.logf(slog.LevelWarn, format, args...) }

// Error logs at error level.
func (l *Logger) Error(args ...any) { l.log(slog.LevelError, 3, args...) }

// Errorf logs a formatted message at error level.
func (l *Logger) Errorf(format string, args ...any) { l.logf(slog.LevelError, format, args...) }

// Fatal logs at fatal level and exits.
func (l *Logger) Fatal(args ...any) {
	l.log(levelFatal, 3, args...)
	os.Exit(1)
}

// Fatalf logs a formatted message at fatal level and exits.
func (l *Logger) Fatalf(format string, args ...any) {
	l.logf(levelFatal, format, args...)
	os.Exit(1)
}

func (l *Logger) log(level slog.Level, skip int, args ...any) {
	if l == nil || l.level == nil || l.level.Level() > level {
		return
	}
	msg := fmt.Sprint(args...)
	if l.level.Level() <= slog.LevelDebug {
		l.logger.LogAttrs(context.Background(), level, msg,
			slog.String("caller", l.caller(skip)))
		return
	}
	l.logger.LogAttrs(context.Background(), level, msg)
}

func (l *Logger) logf(level slog.Level, format string, args ...any) {
	l.log(level, 4, fmt.Sprintf(format, args...))
}

// alwaysf emits a warning even when the configured threshold is error/fatal.
func (l *Logger) alwaysf(format string, args ...any) {
	if l == nil || l.always == nil {
		return
	}
	l.always.With(l.args...).Warn(fmt.Sprintf(format, args...))
}

func (l *Logger) caller(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		file = "<???>"
	} else {
		file = filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file))
	}
	return fmt.Sprintf("%s:%d", file, line)
}

// Apply changes the level and reports a format difference. Loggers that do
// not implement SetLevel (such as test Nop loggers) are ignored.
func Apply(l logger.Logger, level, format string) {
	if l == nil {
		return
	}
	setter, ok := l.(interface {
		SetLevel(string) error
		Format() string
	})
	if !ok {
		return
	}
	formatChanged := !strings.EqualFold(setter.Format(), strings.TrimSpace(format))
	if err := setter.SetLevel(level); err != nil {
		l.Errorf("log level: %v", err)
		return
	}
	if formatChanged {
		msg := fmt.Sprintf("log.format changed to %q; restart required", format)
		if a, ok := l.(interface{ alwaysf(string, ...any) }); ok {
			a.alwaysf("%s", msg)
		} else {
			l.Warnf("%s", msg)
		}
	}
}

func parseLevel(level string) (logger.LogLevel, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return logger.InfoLevel, nil
	case "trace":
		return logger.TraceLevel, nil
	case "debug":
		return logger.DebugLevel, nil
	case "warn":
		return logger.WarnLevel, nil
	case "error":
		return logger.ErrorLevel, nil
	case "fatal":
		return logger.FatalLevel, nil
	default:
		return "", fmt.Errorf("invalid log level %q", level)
	}
}

func parseFormat(format string) (logger.LogFormat, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		return logger.JSONFormat, nil
	case "text":
		return logger.TextFormat, nil
	default:
		return "", fmt.Errorf("invalid log format %q", format)
	}
}

func convertLevel(lvl logger.LogLevel) slog.Level {
	switch lvl {
	case logger.TraceLevel:
		return levelTrace
	case logger.DebugLevel:
		return slog.LevelDebug
	case logger.WarnLevel:
		return slog.LevelWarn
	case logger.ErrorLevel:
		return slog.LevelError
	case logger.FatalLevel:
		return levelFatal
	default:
		return slog.LevelInfo
	}
}

func revertLevel(lvl slog.Level) logger.LogLevel {
	switch {
	case lvl <= levelTrace:
		return logger.TraceLevel
	case lvl <= slog.LevelDebug:
		return logger.DebugLevel
	case lvl <= slog.LevelInfo:
		return logger.InfoLevel
	case lvl <= slog.LevelWarn:
		return logger.WarnLevel
	case lvl <= slog.LevelError:
		return logger.ErrorLevel
	default:
		return logger.FatalLevel
	}
}

func levelString(level slog.Level) string {
	switch {
	case level <= levelTrace:
		return "trace"
	case level <= slog.LevelDebug:
		return "debug"
	case level <= slog.LevelInfo:
		return "info"
	case level <= slog.LevelWarn:
		return "warn"
	case level <= slog.LevelError:
		return "error"
	default:
		return "fatal"
	}
}

// replaceAttr keeps output compatible with the existing x/logger format:
// lowercase level names and millisecond RFC3339 timestamps.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			a.Value = slog.StringValue(t.Format("2006-01-02T15:04:05.000Z07:00"))
		}
	case slog.LevelKey:
		if level, ok := a.Value.Any().(slog.Level); ok {
			a.Value = slog.StringValue(levelString(level))
		}
	}
	return a
}

// mergeArgs merges fields with last-write-wins while preserving insertion
// order, matching the x/logger behavior.
func mergeArgs(args []any, fields map[string]any) []any {
	index := make(map[string]int, len(args)/2+len(fields))
	out := make([]any, 0, len(args)+len(fields)*2)
	for i := 0; i < len(args); i += 2 {
		index[args[i].(string)] = len(out)
		out = append(out, args[i], args[i+1])
	}
	for k, v := range fields {
		if i, ok := index[k]; ok {
			out[i+1] = v
		} else {
			index[k] = len(out)
			out = append(out, k, v)
		}
	}
	return out
}
