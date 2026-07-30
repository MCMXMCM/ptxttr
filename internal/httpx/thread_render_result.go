package httpx

import "ptxt-nstr/internal/nostrx"

type ThreadRenderStatus string

const (
	ThreadRenderReady     ThreadRenderStatus = "ready"
	ThreadRenderPartial   ThreadRenderStatus = "partial"
	ThreadRenderNotFound  ThreadRenderStatus = "not_found"
	ThreadRenderRetryable ThreadRenderStatus = "retryable"
)

// ThreadRenderResult is the single typed completeness contract shared by SSR,
// fragments, cache publication, and response headers.
type ThreadRenderResult struct {
	Status         ThreadRenderStatus
	Root           *nostrx.Event
	Selected       *nostrx.Event
	Ancestors      []nostrx.Event
	VisibleReplies []nostrx.Event
	HiddenReplies  []nostrx.Event
	Cursor         int64
	CursorID       string
	HasMore        bool
	Generation     int64
}

func (r ThreadRenderResult) Ready() bool { return r.Status == ThreadRenderReady }
