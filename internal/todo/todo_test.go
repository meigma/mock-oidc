package todo

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTodo(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.June, 22, 13, 0, 0, 0, time.UTC)
	tooLong := strings.Repeat("a", maxTitleLength) + "x"

	tests := []struct {
		name      string
		title     string
		wantTitle string
		wantErr   error
	}{
		{name: "valid", title: "buy milk", wantTitle: "buy milk", wantErr: nil},
		{name: "trims whitespace", title: "  buy milk  ", wantTitle: "buy milk", wantErr: nil},
		{name: "rejects empty", title: "", wantTitle: "", wantErr: ErrInvalidTitle},
		{name: "rejects whitespace only", title: "   ", wantTitle: "", wantErr: ErrInvalidTitle},
		{name: "rejects too long", title: tooLong, wantTitle: "", wantErr: ErrInvalidTitle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewTodo("id-1", tt.title, now)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "id-1", got.ID)
			assert.Equal(t, tt.wantTitle, got.Title)
			assert.Equal(t, StatusOpen, got.Status)
			assert.Equal(t, now, got.CreatedAt)
			assert.Nil(t, got.CompletedAt)
		})
	}
}

func TestStatusValid(t *testing.T) {
	t.Parallel()

	assert.True(t, StatusOpen.Valid())
	assert.True(t, StatusCompleted.Valid())
	assert.False(t, Status("bogus").Valid())
}

func TestTodoComplete(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.June, 22, 13, 0, 0, 0, time.UTC)
	completedAt := created.Add(time.Hour)

	open, err := NewTodo("id-1", "buy milk", created)
	require.NoError(t, err)

	completed := open.Complete(completedAt)
	assert.Equal(t, StatusCompleted, completed.Status)
	require.NotNil(t, completed.CompletedAt)
	assert.Equal(t, completedAt, *completed.CompletedAt)

	// Value semantics: the original is left unchanged.
	assert.Equal(t, StatusOpen, open.Status)
	assert.Nil(t, open.CompletedAt)

	// Idempotent: completing again preserves the first completion time.
	again := completed.Complete(completedAt.Add(time.Hour))
	require.NotNil(t, again.CompletedAt)
	assert.Equal(t, completedAt, *again.CompletedAt)
}
