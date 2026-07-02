package httpapi

import "net/url"

// FlatForm is the last-wins flat view of an x-www-form-urlencoded body (upstream
// keyValuesToMap('&')): duplicate keys collapse to the LAST value. It drives
// grant dispatch and client-auth decoding. A distinct named type ensures a
// multi-valued [url.Values] can never be passed where flat semantics are
// required; the multi-valued parser (parseFormMulti → oidc.FormParams) lands with
// request-mapping templating in a later slice.
type FlatForm map[string]string

// Get returns the last value for k, or "" when absent.
func (f FlatForm) Get(k string) string { return f[k] }

// Has reports whether k was present in the form (even with an empty value).
func (f FlatForm) Has(k string) bool { _, ok := f[k]; return ok }

// parseFormFlat parses x-www-form-urlencoded bytes into the last-wins flat map.
// It builds on [url.ParseQuery], whose first-'=' split and empty-value handling
// preserve intent without upstream's silent-truncation quirks.
func parseFormFlat(raw []byte) (FlatForm, error) {
	vals, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, err
	}
	out := make(FlatForm, len(vals))
	for k, vs := range vals {
		out[k] = vs[len(vs)-1] // last wins
	}
	return out, nil
}
