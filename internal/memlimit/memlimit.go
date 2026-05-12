// Package memlimit applies a soft heap ceiling when the operator has not set GOMEMLIMIT.
package memlimit

import (
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
)

// DefaultBytes is the soft heap limit when neither GOMEMLIMIT nor PTXT_MEMORY_LIMIT_BYTES is set.
// Matches cmd/server commentary: room for SQLite and headroom on small hosts.
const DefaultBytes int64 = 1 * 1024 * 1024 * 1024

// ApplyDefaultFromEnv sets debug.SetMemoryLimit when GOMEMLIMIT is unset.
// PTXT_MEMORY_LIMIT_BYTES may supply a decimal byte count override.
func ApplyDefaultFromEnv() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	limit := DefaultBytes
	if raw := os.Getenv("PTXT_MEMORY_LIMIT_BYTES"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			slog.Warn("ignoring PTXT_MEMORY_LIMIT_BYTES", "value", raw, "err", err)
		} else {
			limit = parsed
		}
	}
	debug.SetMemoryLimit(limit)
	slog.Info("memory limit applied", "bytes", limit)
}
