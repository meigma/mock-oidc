package controlapi

import "time"

// MintTokenInput is POST /_mock/mint. The forwarding headers feed proxy-aware iss
// resolution (parity with the OIDC edge); the body carries the mint spec.
type MintTokenInput struct {
	Host     string `header:"Host"`
	FwdProto string `header:"X-Forwarded-Proto"`
	FwdHost  string `header:"X-Forwarded-Host"`
	FwdPort  string `header:"X-Forwarded-Port"`
	Body     MintRequestDTO
}

// MintRequestDTO folds upstream's issueToken and anyToken overloads into one body.
type MintRequestDTO struct {
	Issuer    string         `json:"issuer"                  default:"default"      doc:"Issuer id (first segment); '_mock' reserved."`
	IssuerURL string         `json:"issuerUrl,omitempty"                            doc:"Override iss with an arbitrary URL (anyToken)."`
	Subject   string         `json:"subject,omitempty"                              doc:"sub claim; defaults to a random UUID."`
	Audience  []string       `json:"audience,omitempty"                             doc:"aud claim. Omitted -> no audience is stamped."`
	Scope     []string       `json:"scope,omitempty"`
	ClientID  string         `json:"clientId,omitempty"      default:"default"`
	Kind      string         `json:"kind,omitempty"          default:"access_token"                                                        enum:"access_token,id_token"`
	Typ       string         `json:"typ,omitempty"           default:"JWT"          doc:"JWS typ header (open JOSEType; at+jwt accepted)."`
	Claims    map[string]any `json:"claims,omitempty"                               doc:"Additional/overriding claims."`
	ExpirySec *int           `json:"expirySeconds,omitempty" default:"3600"`
}

// MintTokenOutput is the signed artifact plus decoded convenience fields.
type MintTokenOutput struct {
	Body struct {
		Token     string         `json:"token" doc:"Compact signed JWT."`
		Kid       string         `json:"kid"`
		Algorithm string         `json:"algorithm"`
		Issuer    string         `json:"issuer" doc:"Resolved iss."`
		ExpiresAt time.Time      `json:"expiresAt"`
		Claims    map[string]any `json:"claims" doc:"Decoded claim set (convenience)."`
	}
}

// RequestMappingDTO is one request-param -> templated-claims rule. Its presence in
// a ScenarioDTO's requestMappings selects the RequestMappingCallback.
type RequestMappingDTO struct {
	Param      string         `json:"param"                doc:"Form/synthetic param name to test (e.g. client_id, scope, subject)."`
	Match      string         `json:"match"                doc:"'*' (any present value), an exact string, or a full-match regex."`
	TypeHeader string         `json:"typeHeader,omitempty" doc:"Overrides the JWS typ when this mapping matches."`
	Claims     map[string]any `json:"claims,omitempty"     doc:"${...}-templated claims applied when this mapping matches."`
}

// ScenarioDTO is the declarative TokenCallback description — the same JSON shape
// the config `tokenCallbacks` parser produces. With requestMappings it yields a
// RequestMappingCallback; otherwise a DefaultTokenCallback carrying these fields.
type ScenarioDTO struct {
	Issuer          string              `json:"issuer"                    default:"default"`
	Subject         string              `json:"subject,omitempty"`
	Audience        []string            `json:"audience,omitempty"`
	Claims          map[string]any      `json:"claims,omitempty"`
	Typ             string              `json:"typ,omitempty"`
	ExpirySeconds   *int                `json:"expirySeconds,omitempty"`
	RequestMappings []RequestMappingDTO `json:"requestMappings,omitempty"`
}

// EnqueueScenarioInput is POST /_mock/scenarios.
type EnqueueScenarioInput struct {
	Body ScenarioDTO
}

// EnqueueScenarioOutput reports the minted id and the resulting queue depth.
type EnqueueScenarioOutput struct {
	Body struct {
		ScenarioID string `json:"scenarioId"`
		QueueDepth int    `json:"queueDepth"`
	}
}

// ScenarioSummaryDTO is a decoded pending scenario for GET /_mock/scenarios.
type ScenarioSummaryDTO struct {
	Issuer string `json:"issuer"`
	Kind   string `json:"kind"   doc:"'default' or 'requestMapping'."`
}

// ListScenariosOutput is the pending queue (depth + decoded head-first summaries).
type ListScenariosOutput struct {
	Body struct {
		QueueDepth int                  `json:"queueDepth"`
		Scenarios  []ScenarioSummaryDTO `json:"scenarios"`
	}
}

// ClearScenariosOutput confirms the flush.
type ClearScenariosOutput struct {
	Body struct {
		QueueDepth int `json:"queueDepth"`
	}
}

// TakeRequestInput is POST /_mock/requests/take (destructive FIFO long-poll).
type TakeRequestInput struct {
	Body struct {
		TimeoutMs int    `json:"timeoutMs,omitempty" default:"1000" doc:"Max time to wait for a matching request."`
		Issuer    string `json:"issuer,omitempty"`
		Endpoint  string `json:"endpoint,omitempty" enum:"authorize,token,userinfo,introspect,revoke,endsession,jwks"`
	}
}

// TakeRequestOutput carries the dequeued request.
type TakeRequestOutput struct {
	Body CapturedRequestDTO
}

// ListRequestsInput is GET /_mock/requests (non-destructive log) with filters.
type ListRequestsInput struct {
	Issuer   string `query:"issuer"`
	Endpoint string `query:"endpoint" enum:"authorize,token,userinfo,introspect,revoke,endsession,jwks"`
}

// ListRequestsOutput is the arrival-ordered log snapshot.
type ListRequestsOutput struct {
	Body struct {
		Count    int                  `json:"count"`
		Requests []CapturedRequestDTO `json:"requests"`
	}
}

// ClearRequestsOutput confirms the clear.
type ClearRequestsOutput struct {
	Body struct {
		Cleared bool `json:"cleared"`
	}
}

// CapturedRequestDTO is one recorded inbound request. bodyBase64 preserves the
// exact bytes (param order intact); body is a best-effort UTF-8 decode.
type CapturedRequestDTO struct {
	ID         string              `json:"id"`
	ReceivedAt time.Time           `json:"receivedAt"`
	Issuer     string              `json:"issuer"`
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	URL        string              `json:"url"`
	Query      map[string][]string `json:"query,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBase64 string              `json:"bodyBase64,omitempty" doc:"Raw body bytes, base64 (exact order)."`
	Body       string              `json:"body,omitempty"       doc:"Best-effort UTF-8 decode of the body, for convenience."`
}

// ClockStateOutput is GET /_mock/clock (and the return of the mutating clock ops).
type ClockStateOutput struct {
	Body ClockStateDTO
}

// ClockStateDTO snapshots the mutable clock.
type ClockStateDTO struct {
	Frozen bool      `json:"frozen"`
	Now    time.Time `json:"now"`
}

// SetClockInput is PUT /_mock/clock.
type SetClockInput struct {
	Body struct {
		Frozen  bool       `json:"frozen"`
		Instant *time.Time `json:"instant,omitempty" doc:"Required when frozen=true; the fixed 'now'."`
	}
}

// AdvanceClockInput is POST /_mock/clock/advance.
type AdvanceClockInput struct {
	Body struct {
		Duration string `json:"duration" doc:"Go duration, e.g. '90s', '5m', '1h'. Advances (and freezes) the clock."`
	}
}

// ResetOutput confirms a control-plane reset.
type ResetOutput struct {
	Body struct {
		Reset bool `json:"reset"`
	}
}
