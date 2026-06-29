package authz

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUsesEmbeddedBasePoliciesByDefault(t *testing.T) {
	t.Parallel()

	authorizer, err := New(nil)
	require.NoError(t, err)
	assert.NotNil(t, authorizer)
}

func TestNewWithPolicyDirLoadsDirectoryPolicies(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	policy := `permit (principal, action, resource);`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "00_allow.cedar"), []byte(policy), 0o600))

	authorizer, err := New(nil, WithPolicyDir(dir))
	require.NoError(t, err)
	assert.NotNil(t, authorizer)
}

func TestNewWithPolicyDirRejectsMissingDirectory(t *testing.T) {
	t.Parallel()

	_, err := New(nil, WithPolicyDir(filepath.Join(t.TempDir(), "does-not-exist")))
	require.Error(t, err, "a missing policy directory must fail startup, not silently fall back")
}

func TestNewWithPolicyDirRejectsDirectoryWithoutCedarFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a policy"), 0o600))

	_, err := New(nil, WithPolicyDir(dir))
	require.Error(t, err, "a policy directory with no .cedar files must fail rather than drop all base policies")
}

func TestNewWithPolicyDirRejectsInvalidPolicy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.cedar"), []byte("this is not cedar"), 0o600))

	_, err := New(nil, WithPolicyDir(dir))
	require.Error(t, err, "an unparseable policy file must fail startup")
}
