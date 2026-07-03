package oidc

import (
	"errors"
	"fmt"
	"strings"
)

// LoginTemplateName is the config-declared template identifier — the value a
// headless client sends as login_hint. It is a distinct type from Subject so a
// template lookup key is never confused with the identity it resolves to.
type LoginTemplateName string

// LoginTemplate is one named, config-declared principal: selecting it (via the
// login-page dropdown or a login_hint) logs in as Subject with Claims. It is
// deliberately just {name, subject, claims} — no token overrides; those remain
// the domain of token callbacks.
type LoginTemplate struct {
	Name    LoginTemplateName
	Subject Subject
	Claims  CustomClaims
}

// Submission resolves the template into the LoginSubmission the authorize
// pipeline already understands, so template claims inherit login-claim
// semantics (merged putIfAbsent at mint; a mapping/registered claim wins).
// Claims are cloned so per-request merges never mutate the shared template.
func (t LoginTemplate) Submission() LoginSubmission {
	return LoginSubmission{Username: t.Subject, Claims: t.Claims.Clone()}
}

// Config-time login-template sentinels for [errors.Is]. They surface at startup
// via the JSON-config loader (fail-fast), never on a request path.
var (
	ErrBlankTemplateName     = errors.New("login template name must not be blank")
	ErrBlankTemplateSubject  = errors.New("login template subject must not be blank")
	ErrDuplicateTemplateName = errors.New("duplicate login template name")
)

// NewLoginTemplate validates the config-declared template fields: a blank
// (or whitespace-only) name or subject is rejected with the matching sentinel.
// These are config-time errors, not *ProtocolError — a bad template aborts
// startup rather than answering a request.
func NewLoginTemplate(name string, subject Subject, claims CustomClaims) (LoginTemplate, error) {
	if strings.TrimSpace(name) == "" {
		return LoginTemplate{}, ErrBlankTemplateName
	}
	if strings.TrimSpace(string(subject)) == "" {
		return LoginTemplate{}, ErrBlankTemplateSubject
	}
	return LoginTemplate{Name: LoginTemplateName(name), Subject: subject, Claims: claims}, nil
}

// LoginTemplates is the ordered, name-indexed template collection. The zero
// value is valid and empty — an empty collection turns the feature off (the
// login_hint branch is skipped and the dropdown is not rendered). Uniqueness of
// names is a construction invariant (NewLoginTemplates), so Lookup is
// unambiguous by parse-don't-validate.
type LoginTemplates struct {
	order  []LoginTemplateName
	byName map[LoginTemplateName]LoginTemplate
}

// NewLoginTemplates builds the collection preserving declaration order and
// rejecting duplicate names (wrapping ErrDuplicateTemplateName with the
// offending name).
func NewLoginTemplates(templates ...LoginTemplate) (LoginTemplates, error) {
	if len(templates) == 0 {
		return LoginTemplates{}, nil
	}
	ts := LoginTemplates{
		order:  make([]LoginTemplateName, 0, len(templates)),
		byName: make(map[LoginTemplateName]LoginTemplate, len(templates)),
	}
	for _, t := range templates {
		if _, dup := ts.byName[t.Name]; dup {
			return LoginTemplates{}, fmt.Errorf("%w: %q", ErrDuplicateTemplateName, string(t.Name))
		}
		ts.order = append(ts.order, t.Name)
		ts.byName[t.Name] = t
	}
	return ts, nil
}

// Lookup returns the template registered under name, reporting a miss with ok.
func (ts LoginTemplates) Lookup(name LoginTemplateName) (LoginTemplate, bool) {
	t, ok := ts.byName[name]
	return t, ok
}

// Len reports the number of configured templates; zero means the feature is off.
func (ts LoginTemplates) Len() int { return len(ts.order) }

// All returns the templates in declaration order — the login-page render
// accessor. The returned slice is freshly allocated; callers may not reach the
// internal state through it.
func (ts LoginTemplates) All() []LoginTemplate {
	if len(ts.order) == 0 {
		return nil
	}
	all := make([]LoginTemplate, 0, len(ts.order))
	for _, name := range ts.order {
		all = append(all, ts.byName[name])
	}
	return all
}

// UnknownLoginTemplate reports a login_hint that names no configured template
// as invalid_request (400). It is a hard error by design: when templates are
// configured, a typo'd hint failing loudly beats an automated test silently
// receiving a login page or a default-subject token.
func UnknownLoginTemplate(name string) *ProtocolError {
	return MalformedRequest(fmt.Sprintf("login_hint %q does not match a configured login template", name))
}
