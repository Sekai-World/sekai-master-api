// Package cachehint lets handlers tell the response cache how long their
// answer stays valid (docs/postgres-master-data-store.md, "Caching").
//
// A handler whose response depends on the clock (spoiler filtering, the
// current event) reports the next moment the answer changes with
// ValidUntil. The cache stores the response no longer than that. Outside a
// cached request the calls do nothing.
package cachehint

import (
	"context"
	"sync"
	"time"
)

type contextKey struct{}

// Hint collects what a handler reports about its response.
type Hint struct {
	mu    sync.Mutex
	until time.Time
}

// WithHint returns a context that collects hints, and the hint it collects
// them in.
func WithHint(ctx context.Context) (context.Context, *Hint) {
	hint := &Hint{}
	return context.WithValue(ctx, contextKey{}, hint), hint
}

// ValidUntil reports that the response may change at t. The earliest
// reported moment wins; a zero t is ignored.
func ValidUntil(ctx context.Context, t time.Time) {
	if ctx == nil || t.IsZero() {
		return
	}
	hint, ok := ctx.Value(contextKey{}).(*Hint)
	if !ok || hint == nil {
		return
	}
	hint.mu.Lock()
	defer hint.mu.Unlock()
	if hint.until.IsZero() || t.Before(hint.until) {
		hint.until = t
	}
}

// Until returns the earliest reported moment the response may change, and
// whether any was reported.
func (hint *Hint) Until() (time.Time, bool) {
	if hint == nil {
		return time.Time{}, false
	}
	hint.mu.Lock()
	defer hint.mu.Unlock()
	return hint.until, !hint.until.IsZero()
}
