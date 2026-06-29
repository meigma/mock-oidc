package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/meigma/template-go-api/internal/todo"
)

// errInvalidCursor marks a structurally malformed pagination cursor. The list
// handler maps it to 422, so a tampered or stale token is a client error.
var errInvalidCursor = errors.New("invalid cursor")

// cursorPayload is the wire form of a pagination cursor. The cursor is opaque to
// clients: they copy nextCursor back verbatim and must not parse it. The JSON is
// base64url-encoded into a single token.
type cursorPayload struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"id"`
}

// encodeCursor renders a domain cursor as an opaque token, or "" when c is nil.
func encodeCursor(c *todo.Cursor) string {
	if c == nil {
		return ""
	}

	// A {time, string} payload always marshals, so the error is unreachable; an
	// empty token would simply read as "first page" on the way back in.
	raw, _ := json.Marshal(cursorPayload{CreatedAt: c.CreatedAt, ID: c.ID})

	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor parses an opaque token into a domain cursor. An empty token means
// "first page" and yields (nil, nil); a malformed token yields errInvalidCursor.
// It validates structure only (storage-specific checks, e.g. that the id is a
// uuid, belong to the adapter).
func decodeCursor(token string) (*todo.Cursor, error) {
	if token == "" {
		return nil, nil //nolint:nilnil // an empty cursor means "first page": no position and no error.
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errInvalidCursor
	}

	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, errInvalidCursor
	}
	if payload.ID == "" || payload.CreatedAt.IsZero() {
		return nil, errInvalidCursor
	}

	return &todo.Cursor{CreatedAt: payload.CreatedAt, ID: payload.ID}, nil
}
