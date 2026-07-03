package oidc_test

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// TestCoreImportsAreClean enforces the hexagon dependency rule (TDD §5) as
// defense-in-depth beside the depguard "oidc-core" rule: it loads the core's
// non-test import graph and fails on any forbidden transport/framework or
// key-bearing signing package, catching transitive leaks depguard's prefix globs
// can miss.
//
// crypto/sha256, crypto/subtle, and encoding/base64 are deliberately ABSENT from
// the forbidden list: the keyless PKCE S256 transform is pure, keyless domain
// computation (Contract §3 carve-out), kept word-for-word consistent with doc.go
// and the depguard rule so no gate contradicts the PKCE-in-domain decision.
func TestCoreImportsAreClean(t *testing.T) {
	t.Parallel()

	pkgs, err := packages.Load(
		&packages.Config{Mode: packages.NeedImports | packages.NeedName},
		"github.com/meigma/mock-oidc/internal/oidc",
	)
	require.NoError(t, err)
	require.NotEmpty(t, pkgs, "expected to load the core package")

	// Exact matches for the key-bearing signing crypto and transport packages.
	forbiddenExact := []string{
		"net/http", "net/url",
		"crypto/rsa", "crypto/ecdsa", "crypto/ed25519", "crypto/tls", "crypto/x509",
	}
	// Prefixes for framework/vendor trees that must never reach the core.
	forbiddenPrefixes := []string{
		"github.com/danielgtaylor/huma",
		"github.com/go-chi/chi",
		"github.com/go-jose/",
		"github.com/spf13/",
		"go.opentelemetry.io/",
		"github.com/prometheus/",
		"github.com/jackc/pgx",
		"github.com/meigma/mock-oidc/internal/adapter",
	}

	for _, p := range pkgs {
		for imp := range p.Imports {
			for _, bad := range forbiddenExact {
				assert.NotEqual(t, bad, imp, "core %q imports forbidden package %q", p.PkgPath, imp)
			}
			for _, bad := range forbiddenPrefixes {
				assert.Falsef(t, strings.HasPrefix(imp, bad),
					"core %q imports forbidden package %q (matched prefix %q)", p.PkgPath, imp, bad)
			}
		}
	}
}

// TestTransportFreeFiles is a file-scoped guard beside the package-level import
// scan: it parses the request-capture and callback source files directly and
// fails if either names net/url or net/http. capture.go takes only stdlib
// primitives (method/rawURL strings, map[string][]string, []byte) so the httpapi
// edge — not the core — touches *url.URL/http.Header; callback.go's request
// matching and ${...} templating stay pure policy over the domain FormParams. A
// leak here would regress the §3 dependency rule at the exact files the design
// calls out, even before a transitive package-level import appears.
func TestTransportFreeFiles(t *testing.T) {
	t.Parallel()

	forbidden := map[string]struct{}{"net/http": {}, "net/url": {}}
	for _, file := range []string{"capture.go", "callback.go"} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		require.NoErrorf(t, err, "parse %s", file)
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			_, bad := forbidden[path]
			assert.Falsef(t, bad, "%s imports forbidden transport package %q", file, path)
		}
	}
}
