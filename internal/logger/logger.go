package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/lmittmann/tint"
)

// verbose 模式开关
var verbose bool

// Init initialises the logger.
// level should be one of: debug, info, warn, error.
// When verbose is true, all output uses slog format; otherwise, CLI style.
func Init(level string, isVerbose bool) {
	verbose = isVerbose

	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}

	if verbose {
		// Verbose mode: use slog with tint handler
		handler := tint.NewHandler(os.Stderr, &tint.Options{
			Level:   l,
			NoColor: os.Getenv("NO_COLOR") != "",
		})
		slog.SetDefault(slog.New(handler))
	} else {
		// Non-verbose: use minimal handler (only for warnings/errors)
		handler := tint.NewHandler(os.Stderr, &tint.Options{
			Level:      l,
			NoColor:    os.Getenv("NO_COLOR") != "",
			TimeFormat: "", // No timestamp in non-verbose mode
		})
		slog.SetDefault(slog.New(handler))
	}
}

// IsVerbose returns whether verbose mode is enabled.
func IsVerbose() bool {
	return verbose
}

// Print prints a message to stdout (CLI style, no timestamp).
// In verbose mode, it logs as info.
func Print(a ...any) {
	if verbose {
		slog.Info(fmt.Sprint(a...))
	} else {
		_, _ = fmt.Fprint(os.Stdout, a...)
	}
}

// Println prints a message to stdout with newline (CLI style).
// In verbose mode, it logs as info.
func Println(a ...any) {
	if verbose {
		slog.Info(fmt.Sprint(a...))
	} else {
		_, _ = fmt.Fprintln(os.Stdout, a...)
	}
}

// Printf prints a formatted message to stdout (CLI style).
// In verbose mode, it logs as info.
func Printf(format string, a ...any) {
	if verbose {
		slog.Info(fmt.Sprintf(format, a...))
	} else {
		_, _ = fmt.Fprintf(os.Stdout, format, a...)
	}
}

// Error prints an error message to stderr.
// In verbose mode, it uses slog; otherwise, plain text.
func Error(a ...any) {
	if verbose {
		slog.Error(fmt.Sprint(a...))
	} else {
		fmt.Fprint(os.Stderr, a...)
	}
}

// Errorln prints an error message to stderr with newline.
func Errorln(a ...any) {
	if verbose {
		slog.Error(fmt.Sprint(a...))
	} else {
		fmt.Fprintln(os.Stderr, a...)
	}
}

// Errorf prints a formatted error message to stderr.
func Errorf(format string, a ...any) {
	if verbose {
		slog.Error(fmt.Sprintf(format, a...))
	} else {
		fmt.Fprintf(os.Stderr, format, a...)
	}
}

// Debug logs a debug message (only in verbose mode).
func Debug(msg string, args ...any) {
	slog.Debug(msg, args...)
}

// Info logs an info message.
func Info(msg string, args ...any) {
	slog.Info(msg, args...)
}

// Warn logs a warning message.
func Warn(msg string, args ...any) {
	slog.Warn(msg, args...)
}

// Writer returns an io.Writer that writes to the appropriate destination.
// In verbose mode, it logs each write; otherwise, it writes directly.
func Writer(forStdout bool) io.Writer {
	if forStdout {
		return os.Stdout
	}
	return os.Stderr
}
