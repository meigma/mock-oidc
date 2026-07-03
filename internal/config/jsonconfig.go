package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// defaultConfigFilePath is the implicit JSON config source consulted when
// neither JSON_CONFIG nor JSON_CONFIG_PATH is set. A missing file here is not an
// error (it is the zero-config path); a missing JSON_CONFIG_PATH file IS an error
// (the operator named it explicitly).
const defaultConfigFilePath = "config.json"

// Seed is the strongly-typed OIDC behavior parsed from the JSON config document
// (or built-in defaults). Every field is a closed domain type or a primitive
// flag — never map[string]any. The composition root distributes it: SystemTime
// freezes the Clock; Algorithm and InitialKeys drive the signing adapter.
//
// This slice parses the first three JSON-config fields (systemTime, algorithm,
// initialKeys). The remaining upstream OAuth2Config fields (token callbacks,
// login page, TLS) are decoded in later slices.
type Seed struct {
	// SystemTime, when present, freezes the clock at a fixed instant so iat/nbf/
	// exp (and later, verification) are deterministic. Absent → the live,
	// runtime-advanceable clock. SystemTimeFixed reports whether it was set.
	SystemTime      oidc.Instant
	SystemTimeFixed bool

	// Algorithm is the issuer signing algorithm; it defaults to RS256.
	Algorithm oidc.SigningAlgorithm

	// InitialKeys carries opaque private-RSA JWK JSON blobs from config. They are
	// NOT parsed here: crypto lives only in the signing adapter, which parses and
	// validates them (and the algorithm) at construction. Empty → the embedded
	// 5-key RSA seed. An ARRAY is accepted, dropping upstream's single-key JSON
	// limitation.
	InitialKeys [][]byte

	// InteractiveLogin forces GET /authorize to render the interactive login page
	// even when the request does not ask for it (via prompt). Absent → false: the
	// zero-config default auto-issues a code so a stock client's authorize→code→
	// token flow works without a browser, matching upstream.
	InteractiveLogin bool

	// RotateRefreshToken enables refresh-token rotation: on a successful
	// refresh_token redemption the old token is replaced by a fresh RefreshBareUUID
	// token (the nonce is dropped). Absent → false: the same refresh token keeps
	// redeeming, matching upstream's default.
	RotateRefreshToken bool

	// StaticAssetsPath is a filesystem directory served under /static/* (upstream's
	// staticAssetsPath). Empty → the /static tree is not mounted; the built-in
	// login/error pages inline their CSS, so the default deployment needs no static
	// tree. The composition root builds the traversal-guarded file handler from it.
	StaticAssetsPath string

	// TLSFromHTTPServer is true when the JSON config carried an `ssl` object under
	// httpServer (even empty). runServe ORs it into Config.TLSEnabled so an
	// ssl:{} block turns HTTPS on with an in-process self-signed localhost
	// certificate (upstream parity). Cert/key files are never carried here; they
	// come from --tls-cert-file/--tls-key-file on Config.
	TLSFromHTTPServer bool

	// IssuerRecords carries the issuers pre-configured with token callbacks
	// (upstream's tokenCallbacks). Each record groups one issuer's callbacks in
	// declared (first-match) order; the composition root seeds them into the issuer
	// registry so a configured RequestMapping/Default callback shapes that issuer's
	// tokens without a runtime scenario. Empty → every issuer is zero-config
	// (materialized on demand with the built-in default callback).
	IssuerRecords []oidc.IssuerRecord
}

// DefaultSeed is the zero-config seed used when no JSON config is present.
func DefaultSeed() Seed {
	return Seed{Algorithm: oidc.DefaultSigningAlgorithm}
}

// document is the JSON config wire shape (upstream OAuth2Config). Unknown keys
// are ignored (lenient parity); only the fields mock-oidc honors this slice are
// declared.
type document struct {
	TokenProvider      tokenProviderDoc `json:"tokenProvider"`
	InteractiveLogin   bool             `json:"interactiveLogin"`
	RotateRefreshToken bool             `json:"rotateRefreshToken"`
	// StaticAssetsPath is a directory served under /static/* (upstream parity).
	StaticAssetsPath string `json:"staticAssetsPath"`
	// TokenCallbacks is the declarative per-request callback list (upstream's
	// tokenCallbacks). Each entry is the SAME JSON shape the control plane's
	// enqueue-scenario DTO accepts, so a callback described as JSON parses
	// identically whether it arrives at startup (here) or at runtime (/_mock).
	TokenCallbacks []callbackDoc `json:"tokenCallbacks"`
	// HTTPServer mirrors upstream's httpServer field (a bare string or an object
	// carrying an optional `ssl` block). Its presence with an ssl object turns
	// HTTPS on; the concrete server type is otherwise ignored.
	HTTPServer httpServerDoc `json:"httpServer"`
}

// httpServerDoc accepts either upstream's bare-string httpServer form (for
// example "MockWebServerWrapper") or an object with an optional `ssl` block.
// tlsRequested reports whether an ssl object was present (even {}), which
// runServe maps onto Config.TLSEnabled.
type httpServerDoc struct {
	sslPresent bool
}

// UnmarshalJSON parses the string-or-object httpServer shape without holding a
// map[string]any: a bare string form carries no ssl; an object form is probed
// only for a non-null `ssl` member.
func (h *httpServerDoc) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" || trimmed[0] == '"' {
		return nil // absent, null, or the bare-string form: no ssl
	}
	var obj struct {
		SSL json.RawMessage `json:"ssl"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return fmt.Errorf("httpServer: %w", err)
	}
	if len(obj.SSL) > 0 && string(bytes.TrimSpace(obj.SSL)) != "null" {
		h.sslPresent = true
	}
	return nil
}

// tlsRequested reports whether the config asked for HTTPS via an ssl object. It
// takes a pointer receiver to match UnmarshalJSON (a mixed receiver set is a lint
// smell), even though it only reads.
func (h *httpServerDoc) tlsRequested() bool { return h.sslPresent }

// callbackDoc is one declarative token callback. With requestMappings it yields a
// RequestMappingCallback; otherwise a DefaultTokenCallback carrying the
// subject/audience/typ/claims/expiry fields. It mirrors controlapi.ScenarioDTO so
// the two JSON entry points share one wire shape.
type callbackDoc struct {
	Issuer          string              `json:"issuer"`
	Subject         string              `json:"subject"`
	Audience        []string            `json:"audience"`
	Claims          map[string]any      `json:"claims"`
	Typ             string              `json:"typ"`
	ExpirySeconds   *int                `json:"expirySeconds"`
	RequestMappings []requestMappingDoc `json:"requestMappings"`
}

// requestMappingDoc is one param→templated-claims rule (upstream's
// requestMappings). Its presence in a callbackDoc selects the RequestMappingCallback.
type requestMappingDoc struct {
	Param      string         `json:"param"`
	Match      string         `json:"match"`
	TypeHeader string         `json:"typeHeader"`
	Claims     map[string]any `json:"claims"`
}

type tokenProviderDoc struct {
	SystemTime  string         `json:"systemTime"` // RFC3339; "" → live clock
	KeyProvider keyProviderDoc `json:"keyProvider"`
}

type keyProviderDoc struct {
	Algorithm   string            `json:"algorithm"`   // "" → RS256
	InitialKeys []json.RawMessage `json:"initialKeys"` // array of JWK objects
}

// LoadSeed resolves the JSON config source and parses it into a typed Seed. No
// source present → DefaultSeed(). Malformed JSON or any value the domain rejects
// (bad algorithm, unparseable systemTime) is a hard error.
//
// Source precedence (highest first): JSON_CONFIG (inline) > JSON_CONFIG_PATH
// (file) > ./config.json > built-in defaults.
func LoadSeed(vp *viper.Viper) (Seed, error) {
	raw, ok, err := resolveJSONSource(vp)
	if err != nil {
		return Seed{}, err
	}
	if !ok {
		return DefaultSeed(), nil
	}

	var doc document
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return Seed{}, fmt.Errorf("parse JSON config: %w", err)
	}
	return doc.toSeed()
}

// resolveJSONSource implements the source precedence: JSON_CONFIG (inline) >
// JSON_CONFIG_PATH (file) > ./config.json > none. A missing JSON_CONFIG_PATH file
// is an error (it was named explicitly); a missing ./config.json is not (it is
// the implicit zero-config fallback).
func resolveJSONSource(vp *viper.Viper) ([]byte, bool, error) {
	if inline := strings.TrimSpace(vp.GetString("json-config")); inline != "" {
		return []byte(inline), true, nil
	}
	if path := strings.TrimSpace(vp.GetString("json-config-path")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("read json-config-path %q: %w", path, err)
		}
		return b, true, nil
	}
	b, err := os.ReadFile(defaultConfigFilePath)
	switch {
	case err == nil:
		return b, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("read %s: %w", defaultConfigFilePath, err)
	}
}

// toSeed maps the wire document to the typed Seed at the config edge, so the
// domain only ever sees oidc.* values (parse-don't-validate).
func (d document) toSeed() (Seed, error) {
	seed := DefaultSeed()

	if t := strings.TrimSpace(d.TokenProvider.SystemTime); t != "" {
		inst, err := oidc.ParseInstant(t)
		if err != nil {
			return Seed{}, fmt.Errorf("tokenProvider.systemTime: %w", err)
		}
		seed.SystemTime, seed.SystemTimeFixed = inst, true
	}

	if a := strings.TrimSpace(d.TokenProvider.KeyProvider.Algorithm); a != "" {
		alg, err := oidc.ParseSigningAlgorithm(a)
		if err != nil {
			return Seed{}, fmt.Errorf("tokenProvider.keyProvider.algorithm: %w", err)
		}
		seed.Algorithm = alg
	}

	for _, k := range d.TokenProvider.KeyProvider.InitialKeys {
		seed.InitialKeys = append(seed.InitialKeys, []byte(k)) // opaque; parsed by the signing adapter
	}

	seed.InteractiveLogin = d.InteractiveLogin
	seed.RotateRefreshToken = d.RotateRefreshToken
	seed.StaticAssetsPath = strings.TrimSpace(d.StaticAssetsPath)
	seed.TLSFromHTTPServer = d.HTTPServer.tlsRequested() // ssl:{} → TLS on (ORed by runServe)

	records, err := toIssuerRecords(d.TokenCallbacks)
	if err != nil {
		return Seed{}, err
	}
	seed.IssuerRecords = records

	return seed, nil
}

// toIssuerRecords groups the declarative callbacks by issuer into IssuerRecords,
// preserving each issuer's declared (first-match) order. Grouping lets the token
// service walk one issuer's configured callbacks in the order the operator wrote
// them; the FIRST that matches wins (before the built-in default).
func toIssuerRecords(docs []callbackDoc) ([]oidc.IssuerRecord, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	order := make([]oidc.IssuerID, 0, len(docs))
	byIssuer := make(map[oidc.IssuerID][]oidc.TokenCallback, len(docs))
	for i, doc := range docs {
		issuer, cb, err := doc.toCallback()
		if err != nil {
			return nil, fmt.Errorf("tokenCallbacks[%d]: %w", i, err)
		}
		if _, seen := byIssuer[issuer]; !seen {
			order = append(order, issuer)
		}
		byIssuer[issuer] = append(byIssuer[issuer], cb)
	}

	records := make([]oidc.IssuerRecord, 0, len(order))
	for _, issuer := range order {
		records = append(records, oidc.IssuerRecord{ID: issuer, Callbacks: byIssuer[issuer]})
	}
	return records, nil
}

// toCallback maps one declarative callback onto the SAME domain constructors the
// control-plane scenario mapping calls (oidc.NewRequestMappingCallback /
// oidc.NewDefaultTokenCallbackWith), so config and control share one
// anti-corruption path for "a callback described as JSON". requestMappings present
// → a RequestMappingCallback; otherwise a DefaultTokenCallback carrying the
// entry's subject/audience/typ/claims/expiry.
func (d callbackDoc) toCallback() (oidc.IssuerID, oidc.TokenCallback, error) {
	issuer, err := oidc.ParseIssuerID(d.Issuer)
	if err != nil {
		return "", nil, err
	}
	var expiry time.Duration
	if d.ExpirySeconds != nil {
		expiry = time.Duration(*d.ExpirySeconds) * time.Second
	}

	if len(d.RequestMappings) > 0 {
		mappings := make([]oidc.RequestMapping, 0, len(d.RequestMappings))
		for _, m := range d.RequestMappings {
			claims, cErr := oidc.NewClaimSet(m.Claims)
			if cErr != nil {
				return "", nil, cErr
			}
			mappings = append(mappings, oidc.RequestMapping{
				Param:      m.Param,
				Match:      m.Match,
				TypeHeader: oidc.JOSEType(m.TypeHeader),
				Claims:     claims.Custom,
			})
		}
		cb, mErr := oidc.NewRequestMappingCallback(issuer, expiry, mappings)
		if mErr != nil {
			return "", nil, mErr
		}
		return issuer, cb, nil
	}

	claims, err := oidc.NewClaimSet(d.Claims)
	if err != nil {
		return "", nil, err
	}
	return issuer, oidc.NewDefaultTokenCallbackWith(
		issuer,
		oidc.Subject(d.Subject),
		oidc.Audience(d.Audience),
		oidc.JOSEType(d.Typ),
		claims.Custom,
		expiry,
	), nil
}
