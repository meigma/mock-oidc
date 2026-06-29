// Package problem writes RFC 9457 application/problem+json error responses for
// transport surfaces that originate outside Huma (router fallbacks, panics, and
// timeouts), so every error response shares the title/status/detail shape Huma
// emits. It is a small leaf shared by the router and the middleware package.
package problem

import (
	"encoding/json"
	"net/http"
)

// ContentType is the RFC 9457 media type for problem-details responses.
const ContentType = "application/problem+json"

// details is the RFC 9457 problem document. It mirrors the title/status/detail
// members Huma emits so clients see one consistent error shape.
type details struct {
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

// Write writes an RFC 9457 application/problem+json error response.
func Write(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(details{
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	})
}
