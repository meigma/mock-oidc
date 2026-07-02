package httpapi

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
)

// htmlContentType is the Content-Type stamped on every rendered browser page.
const htmlContentType = "text/html; charset=utf-8"

// The template names, matching the embedded file basenames.
const (
	tmplLogin    = "login.html"
	tmplFormPost = "form_post.html"
	tmplError    = "error.html"
)

// htmlFS embeds the browser-surface templates. They render only through
// html/template so every user-influenced value (redirect_uri, state, the login
// action URL, error text) is context-auto-escaped — never string-concatenated
// into HTML.
//
//go:embed html/*.html
var htmlFS embed.FS

// templates is parsed once at package init from the embedded FS. A parse failure
// is a programming error (the templates ship in the binary), so it panics.
//
//nolint:gochecknoglobals // parsed-once template set for the browser surface.
var templates = template.Must(template.ParseFS(htmlFS, "html/*.html"))

// BrowserOutput is the shared Huma output envelope for every browser-facing
// operation (GET/POST /authorize, favicon). The HANDLER — not the framework —
// sets Status: a 302 redirect carries only Location; an HTML page carries
// ContentType + Body. Huma writes a []byte Body raw (no JSON marshaling) and
// skips empty headers, so one envelope covers redirect, HTML, and empty-200.
type BrowserOutput struct {
	Status      int
	Location    string `header:"Location"`
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// loginData is the login.html model: the action URL the form POSTs back to
// (the same /authorize URL, query string preserved).
type loginData struct {
	Action string
}

// formPostData is the form_post.html model: the self-submitting form posts ONLY
// code (+ state when present) to the redirect_uri.
type formPostData struct {
	RedirectURI string
	Code        string
	State       string
}

// errorData is the error.html model for the direct (non-redirect) error page.
type errorData struct {
	Error       string
	Description string
}

// htmlOutput renders the named template into a BrowserOutput at the given status.
// A render failure falls back to a plain-text 500 rather than leaking a partial
// document, so the browser surface never emits half-escaped HTML.
func htmlOutput(status int, name string, data any) *BrowserOutput {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		return &BrowserOutput{
			Status:      http.StatusInternalServerError,
			ContentType: "text/plain; charset=utf-8",
			Body:        []byte("template render error"),
		}
	}
	return &BrowserOutput{Status: status, ContentType: htmlContentType, Body: buf.Bytes()}
}
