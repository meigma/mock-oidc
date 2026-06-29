package todo

import (
	"errors"
	"time"
)

// Page-size bounds for List. The HTTP transport enforces MaxPageSize at the edge
// (a static maximum on the limit query parameter) and the service clamps
// defensively, so per-request work is bounded even for a direct (non-HTTP)
// caller. Keep MaxPageSize/DefaultPageSize in sync with the maximum/default tags
// on httpapi.ListTodosInput.Limit — a unit test asserts they agree.
const (
	// DefaultPageSize is the page size applied when a caller requests none.
	DefaultPageSize = 20
	// MaxPageSize is the largest page a caller may request.
	MaxPageSize = 100
)

// ErrInvalidCursor indicates a pagination cursor could not be interpreted (for
// example a tampered or stale token). The HTTP layer maps it to 422, so a bad
// cursor is a client error rather than a 500.
var ErrInvalidCursor = errors.New("invalid todo cursor")

// Cursor is a keyset position in the (CreatedAt, ID) ordering that List walks. It
// marks the last item of a page; the next page is the rows strictly after it. It
// is opaque to clients — the transport encodes it as a single token.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// PageQuery bounds a List call. The service clamps Limit into [1, MaxPageSize]
// (a non-positive Limit means DefaultPageSize), so adapters may assume Limit >= 1.
// After is the keyset position to resume from; nil requests the first page.
type PageQuery struct {
	Limit int
	After *Cursor
}

// PageResult is one bounded page of todos plus the cursor for the next page. Next
// is nil when the page is the last one.
type PageResult struct {
	Todos []Todo
	Next  *Cursor
}
