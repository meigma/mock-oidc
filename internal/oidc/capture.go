package oidc

import "strings"

// CapturedRequest is the transport-free record of one inbound protocol request,
// stored for control-plane inspection (takeRequest). It names ONLY stdlib
// primitives and domain types — never [net/url.URL] or [net/http.Header]: the
// httpapi recording middleware converts transport values to plain
// map[string][]string / string / []byte at the edge and calls
// NewCapturedRequest, so this file (and the core) stays free of net/url and
// net/http (contract §3; arch_test forbids both here). Raw body bytes are
// preserved verbatim — never reparsed — because duplicate keys and param order
// matter to the takeRequest contract.
//
// ID and ReceivedAt are metadata the recording adapter stamps on storage; the
// edge constructor leaves them zero.
type CapturedRequest struct {
	ID         string
	ReceivedAt Instant
	Issuer     IssuerID
	Method     string
	URL        string // the raw request URL (r.URL.String())
	Path       string // the URL path, derived from URL without net/url
	Query      map[string][]string
	Header     map[string][]string
	Body       []byte
}

// NewCapturedRequest builds a CapturedRequest from stdlib primitives supplied by
// the httpapi edge. It derives the path and first-segment issuer from rawURL
// using string operations only (net/url is banned in the core). The body bytes
// are stored verbatim.
func NewCapturedRequest(method, rawURL string, header, query map[string][]string, body []byte) CapturedRequest {
	path := urlPath(rawURL)
	return CapturedRequest{
		Issuer: firstSegment(path),
		Method: method,
		URL:    rawURL,
		Path:   path,
		Query:  query,
		Header: header,
		Body:   body,
	}
}

// urlPath extracts the path component of a raw URL string without net/url: it
// strips any fragment and query, then any scheme://host authority prefix.
func urlPath(rawURL string) string {
	s := rawURL
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if _, rest, found := strings.Cut(s, "://"); found {
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			return rest[j:]
		}
		return "/"
	}
	return s
}

// firstSegment returns the first non-empty path segment as an IssuerID (the
// issuer every protocol path is scoped by), or "" when the path has none.
func firstSegment(path string) IssuerID {
	seg, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return IssuerID(seg)
}

// CaptureFilter narrows a request-log query by issuer and/or endpoint. A zero
// value matches everything; an empty field is a wildcard for that dimension.
// Endpoint is the trailing path segment (e.g. "token", "authorize"), matching
// the control API's takeRequest endpoint enum.
type CaptureFilter struct {
	Issuer   IssuerID
	Endpoint string
}

// Matches reports whether req satisfies the filter.
func (f CaptureFilter) Matches(req CapturedRequest) bool {
	if f.Issuer != "" && req.Issuer != f.Issuer {
		return false
	}
	if f.Endpoint != "" && !strings.HasSuffix(req.Path, "/"+f.Endpoint) {
		return false
	}
	return true
}
