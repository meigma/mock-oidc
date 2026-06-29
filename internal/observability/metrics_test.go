package observability

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		want   string
	}{
		{name: "get", method: http.MethodGet, want: http.MethodGet},
		{name: "post", method: http.MethodPost, want: http.MethodPost},
		{name: "delete", method: http.MethodDelete, want: http.MethodDelete},
		{name: "unknown token", method: "BREW", want: methodOther},
		{name: "arbitrary token", method: "AAAA", want: methodOther},
		{name: "empty", method: "", want: methodOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, normalizeMethod(tt.method))
		})
	}
}
