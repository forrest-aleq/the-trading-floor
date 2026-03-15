package observe

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// RecoverPanic logs a panic and stack trace instead of letting a background goroutine die silently.
func RecoverPanic(log *slog.Logger, message string, fields ...any) {
	if r := recover(); r != nil {
		if log == nil {
			log = slog.Default()
		}
		payload := append([]any{}, fields...)
		payload = append(payload,
			"panic", fmt.Sprintf("%v", r),
			"stack", string(debug.Stack()),
		)
		log.Error(message, payload...)
	}
}

// SafeGo launches a goroutine with panic recovery and structured logging.
func SafeGo(log *slog.Logger, message string, fn func(), fields ...any) {
	go func() {
		defer RecoverPanic(log, message, fields...)
		fn()
	}()
}
