package authz

import (
	_ "embed"

	"github.com/meigma/template-go-api/internal/authz"
	"github.com/meigma/template-go-api/internal/todo"
)

// policy holds the slice's embedded Cedar policies, merged into the runtime
// PolicySet by the composition root. Embedding keeps the slice self-contained:
// the policies ship in the binary and need no external file.
//
//go:embed policy.cedar
var policy []byte

// Contribution returns the todo slice's input to the authorization engine: its
// embedded policies, the actions it declares, the Todo entity type it owns, and
// a repository-backed resolver factory. The composition root collects it (with
// every other slice's) and merges them in authz.New.
//
// repo is the same todo.Repository the HTTP slice uses, so an attribute policy
// resolves a todo's facts from the one source of truth — lazily, only when a
// policy dereferences a Todo entity. The shipped coarse policy needs no load, so
// the resolver is never invoked by default.
func Contribution(repo todo.Repository) authz.Contribution {
	return authz.Contribution{
		Policies: policy,
		Actions:  actions(),
		Types:    []string{string(TodoType)},
		Resolver: newResolver(repo),
	}
}
