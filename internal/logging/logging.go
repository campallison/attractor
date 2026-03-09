package logging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
)

var logFile *os.File

// multiHandler fans out each log record to two handlers: one for the terminal
// (text, INFO level) and one for the log file (JSON, DEBUG level).
type multiHandler struct {
	terminal slog.Handler
	file     slog.Handler
}

func (h *multiHandler) Enabled(_ context.Context, level slog.Level) bool {
	return h.terminal.Enabled(context.Background(), level) ||
		h.file.Enabled(context.Background(), level)
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.terminal.Enabled(ctx, r.Level) {
		_ = h.terminal.Handle(ctx, r)
	}
	if h.file.Enabled(ctx, r.Level) {
		_ = h.file.Handle(ctx, r)
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &multiHandler{
		terminal: h.terminal.WithAttrs(attrs),
		file:     h.file.WithAttrs(attrs),
	}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	return &multiHandler{
		terminal: h.terminal.WithGroup(name),
		file:     h.file.WithGroup(name),
	}
}

// Setup configures the default slog logger with two outputs:
//   - stderr: human-readable text at INFO level
//   - logFilePath: JSON at DEBUG level (for post-mortem analysis)
//
// If logFilePath is empty, only the terminal handler is created.
// Call Teardown to close the log file when done.
func Setup(logFilePath string) error {
	terminalHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	if logFilePath == "" {
		slog.SetDefault(slog.New(terminalHandler))
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(logFilePath), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	logFile = f

	fileHandler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	slog.SetDefault(slog.New(&multiHandler{
		terminal: terminalHandler,
		file:     fileHandler,
	}))

	return nil
}

// Teardown closes the log file opened by Setup. Safe to call if Setup was never
// called or if no log file was opened.
func Teardown() {
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}
