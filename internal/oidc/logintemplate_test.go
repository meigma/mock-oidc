package oidc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestNewLoginTemplateValidation covers the config-time field checks: blank or
// whitespace-only names and subjects are rejected with their sentinels.
func TestNewLoginTemplateValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tmplName string
		subject  oidc.Subject
		wantErr  error
	}{
		{"valid", "admin-alice", "alice", nil},
		{"blank name", "", "alice", oidc.ErrBlankTemplateName},
		{"whitespace name", "   ", "alice", oidc.ErrBlankTemplateName},
		{"blank subject", "admin-alice", "", oidc.ErrBlankTemplateSubject},
		{"whitespace subject", "admin-alice", "  ", oidc.ErrBlankTemplateSubject},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tmpl, err := oidc.NewLoginTemplate(tc.tmplName, tc.subject, oidc.CustomClaims{})
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, oidc.LoginTemplateName(tc.tmplName), tmpl.Name)
			assert.Equal(t, tc.subject, tmpl.Subject)
		})
	}
}

// TestNewLoginTemplatesRejectsDuplicates asserts the unique-name construction
// invariant, naming the offender in the error.
func TestNewLoginTemplatesRejectsDuplicates(t *testing.T) {
	t.Parallel()

	a, err := oidc.NewLoginTemplate("dup", "alice", oidc.CustomClaims{})
	require.NoError(t, err)
	b, err := oidc.NewLoginTemplate("dup", "bob", oidc.CustomClaims{})
	require.NoError(t, err)

	_, err = oidc.NewLoginTemplates(a, b)
	require.ErrorIs(t, err, oidc.ErrDuplicateTemplateName)
	assert.Contains(t, err.Error(), `"dup"`)
}

// TestLoginTemplatesLookupAndOrder covers Lookup hit/miss, Len, and All
// preserving declaration order; the zero value is empty and misses safely.
func TestLoginTemplatesLookupAndOrder(t *testing.T) {
	t.Parallel()

	b, err := oidc.NewLoginTemplate("basic-bob", "bob", oidc.CustomClaims{})
	require.NoError(t, err)
	a, err := oidc.NewLoginTemplate("admin-alice", "alice", oidc.CustomClaims{})
	require.NoError(t, err)
	templates, err := oidc.NewLoginTemplates(b, a) // declared bob-first on purpose
	require.NoError(t, err)

	assert.Equal(t, 2, templates.Len())

	got, ok := templates.Lookup("admin-alice")
	require.True(t, ok)
	assert.Equal(t, oidc.Subject("alice"), got.Subject)
	_, ok = templates.Lookup("nobody")
	assert.False(t, ok)

	all := templates.All()
	require.Len(t, all, 2)
	assert.Equal(t, oidc.LoginTemplateName("basic-bob"), all[0].Name)
	assert.Equal(t, oidc.LoginTemplateName("admin-alice"), all[1].Name)

	var zero oidc.LoginTemplates
	assert.Equal(t, 0, zero.Len())
	assert.Nil(t, zero.All())
	_, ok = zero.Lookup("admin-alice")
	assert.False(t, ok)
}

// TestLoginTemplateSubmissionClonesClaims guards the aliasing bug: mutating a
// resolved submission's claims must not leak back into the shared template.
func TestLoginTemplateSubmissionClonesClaims(t *testing.T) {
	t.Parallel()

	var claims oidc.CustomClaims
	claims.Set("email", "alice@example.com")
	tmpl, err := oidc.NewLoginTemplate("admin-alice", "alice", claims)
	require.NoError(t, err)

	sub := tmpl.Submission()
	assert.Equal(t, oidc.Subject("alice"), sub.Username)
	sub.Claims.Set("email", "tampered@example.com")
	sub.Claims.Set("extra", true)

	orig, ok := tmpl.Claims.Get("email")
	require.True(t, ok)
	assert.Equal(t, "alice@example.com", orig, "template claims must be unaffected by submission mutation")
	_, ok = tmpl.Claims.Get("extra")
	assert.False(t, ok)
}
