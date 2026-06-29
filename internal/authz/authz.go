package authz

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cedar-policy/cedar-go"
)

// basePolicies holds the cross-cutting policies merged ahead of every slice's
// contribution. Embedded so the template authorizes out of the box with no
// external policy files.
//
//go:embed base.cedar
var basePolicies []byte

// baseSlice is the synthetic slice name used to prefix policy IDs from
// base.cedar during the merge, keeping them distinct from any domain slice.
const baseSlice = "base"

// Authorizer evaluates a Cedar request against the merged PolicySet. It is the
// single decision point the middleware calls; the resolver/getter supplies the
// entities Cedar dereferences on demand.
type Authorizer struct {
	policies *cedar.PolicySet
	// contributions is retained so the middleware can build the request-scoped
	// composite getter from each slice's ResolverFactory.
	contributions []Contribution
}

// Option configures how New builds the Authorizer.
type Option func(*config)

type config struct {
	// policyDir, when non-empty, replaces the embedded base.cedar with the
	// .cedar files loaded from this directory.
	policyDir string
}

// WithPolicyDir loads the base policies from the .cedar files in dir instead of
// the embedded base.cedar. An empty dir keeps the embedded default. The files
// are read once at construction (startup), sorted by name for a deterministic
// merge order.
func WithPolicyDir(dir string) Option {
	return func(c *config) {
		c.policyDir = dir
	}
}

// New merges the base policies and every contribution's policies into one
// runtime PolicySet and returns an Authorizer. The base policies are the
// embedded base.cedar unless WithPolicyDir overrides them with a directory of
// .cedar files. Policy IDs are re-assigned with a slice-prefixed, per-slice
// index ("<slice>#<n>") so policies stay unique across slices after the merge.
// Passing no contributions yields an authorizer with only the base policies.
func New(contributions []Contribution, opts ...Option) (*Authorizer, error) {
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}

	if err := validateTypeOwnership(contributions); err != nil {
		return nil, err
	}

	base, err := loadBasePolicies(cfg.policyDir)
	if err != nil {
		return nil, err
	}

	merged := cedar.NewPolicySet()

	if err := mergePolicies(merged, baseSlice, base); err != nil {
		return nil, fmt.Errorf("merge base policies: %w", err)
	}

	for i, c := range contributions {
		if len(c.Policies) == 0 {
			continue
		}
		// Index the slice by position so two slices that omit a name still get
		// distinct policy-ID prefixes.
		slice := fmt.Sprintf("slice%d", i)
		if err := mergePolicies(merged, slice, c.Policies); err != nil {
			return nil, fmt.Errorf("merge contribution %d policies: %w", i, err)
		}
	}

	// Prepend the always-present principal resolver so cross-cutting and slice
	// policies can test principal group membership (principal in Role::"…")
	// without any slice contributing a principal resolver.
	all := append([]Contribution{principalContribution()}, contributions...)

	return &Authorizer{policies: merged, contributions: all}, nil
}

// validateTypeOwnership enforces, at construction, that entity-type ownership is
// unambiguous across the merged engine. It is the fail-fast guard behind the
// composite getter's single-owner-per-type routing:
//   - No slice may claim a reserved principal type (PrincipalType/AnonymousType),
//     so a slice resolver can never shadow the always-present principal resolver.
//   - No two slices may claim the same type, so a lookup routes to exactly one
//     resolver and a type's facts have a single source of truth.
//   - A slice supplying a Resolver must declare the types it owns: the composite
//     getter routes purely by Types, so a resolver with no declared types is never
//     invoked and any policy dereferencing its entities silently fails closed.
//
// A misconfigured contribution set therefore fails startup rather than silently
// shadowing facts or the principal at request time.
func validateTypeOwnership(contributions []Contribution) error {
	reservedNames := reservedTypes()
	reserved := make(map[string]struct{}, len(reservedNames))
	for _, t := range reservedNames {
		reserved[t] = struct{}{}
	}

	owner := make(map[string]int)
	for i, c := range contributions {
		if c.Resolver != nil && len(c.Types) == 0 {
			return fmt.Errorf(
				"contribution %d supplies a Resolver but declares no Types: a resolver is routed only by the types it owns, so it would never be invoked",
				i,
			)
		}
		for _, t := range c.Types {
			if _, isReserved := reserved[t]; isReserved {
				return fmt.Errorf(
					"contribution %d claims reserved principal type %q: it is owned by the base principal resolver and cannot be overridden",
					i,
					t,
				)
			}
			if prev, dup := owner[t]; dup {
				return fmt.Errorf(
					"contributions %d and %d both claim entity type %q: each Cedar entity type must be owned by exactly one slice",
					prev,
					i,
					t,
				)
			}
			owner[t] = i
		}
	}

	return nil
}

// loadBasePolicies returns the base policy source: the .cedar files concatenated
// from dir when dir is non-empty, otherwise the embedded base.cedar. The files
// are read in sorted name order so the merge (and policy IDs) are deterministic.
// An empty or .cedar-free directory is an error, so a misconfigured policy
// directory fails startup rather than silently dropping every base policy.
func loadBasePolicies(dir string) ([]byte, error) {
	if dir == "" {
		return basePolicies, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read policy directory %q: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".cedar" {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("policy directory %q contains no .cedar files", dir)
	}
	sort.Strings(names)

	var document []byte
	for _, name := range names {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read policy file %q: %w", name, err)
		}
		document = append(document, content...)
		document = append(document, '\n')
	}

	return document, nil
}

// mergePolicies parses document and adds each policy to dst under a
// slice-prefixed ID ("<slice>#<n>"), so merged policy IDs are unique and trace
// back to their source slice.
func mergePolicies(dst *cedar.PolicySet, slice string, document []byte) error {
	list, err := cedar.NewPolicyListFromBytes(slice+".cedar", document)
	if err != nil {
		return fmt.Errorf("parse policies: %w", err)
	}

	for n, policy := range list {
		id := cedar.PolicyID(fmt.Sprintf("%s#%d", slice, n))
		dst.Add(id, policy)
	}

	return nil
}

// Authorize evaluates req against the merged PolicySet using entities, returning
// Cedar's decision and diagnostic. entities is the request-scoped composite
// getter; Cedar pulls only the entities the applicable policies dereference.
func (a *Authorizer) Authorize(entities cedar.EntityGetter, req cedar.Request) (cedar.Decision, cedar.Diagnostic) {
	return cedar.Authorize(a.policies, entities, req)
}

// Contributions returns the slices the authorizer was built from, so the
// middleware can assemble the per-request composite getter.
func (a *Authorizer) Contributions() []Contribution {
	return a.contributions
}
