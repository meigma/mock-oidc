package authz

import (
	"context"
	"errors"

	"github.com/cedar-policy/cedar-go/types"

	"github.com/meigma/template-go-api/internal/authz"
	"github.com/meigma/template-go-api/internal/todo"
)

// TodoType is the Cedar entity type for a todo. It matches the type-level
// resource the base engine derives from the action's "todo:" prefix, so a
// Todo::"<id>" instance bound from the path parameter and a Todo lookup made by
// an attribute policy refer to the same entity space.
const TodoType types.EntityType = "Todo"

// resolver is the todo slice's request-scoped fact resolver. It loads a todo
// from the repository on demand — only when an applicable policy dereferences a
// Todo entity's attributes — and maps it to a Cedar entity. Coarse policies (the
// shipped default) decide without touching it, so no load happens.
//
// It is bound to the request context at construction (Cedar's Get carries no
// context), and reports a load failure through authz.RecordLoadError rather than
// a return value (Get cannot return an error), so the middleware fails closed on
// a backend error instead of trusting a decision made on missing data.
type resolver struct {
	ctx  context.Context
	repo todo.Repository
}

// newResolver builds the per-request todo resolver bound to ctx and the slice's
// repository. It satisfies authz.ResolverFactory; the principal is unused
// because a todo's facts come from the repository, not the caller.
func newResolver(repo todo.Repository) authz.ResolverFactory {
	return func(ctx context.Context, _ authz.Principal) authz.EntityResolver {
		return &resolver{ctx: ctx, repo: repo}
	}
}

// Types reports the entity type this resolver owns, so the composite getter
// routes Todo lookups here.
func (r *resolver) Types() []string {
	return []string{string(TodoType)}
}

// Resolve loads the todo named by uid and maps it to a Cedar entity. A uid of a
// type this resolver does not own, or a todo that does not exist, is a miss
// (false) — not an error. A backend failure is recorded via
// authz.RecordLoadError so the middleware fails closed, and reported as a miss.
func (r *resolver) Resolve(uid types.EntityUID) (types.Entity, bool) {
	if uid.Type != TodoType {
		return types.Entity{}, false
	}

	found, err := r.repo.FindByID(r.ctx, string(uid.ID))
	if err != nil {
		if errors.Is(err, todo.ErrNotFound) {
			return types.Entity{}, false
		}
		// A real backend failure: record it so the middleware returns 500 rather
		// than letting Cedar decide on a missing entity.
		authz.RecordLoadError(r.ctx, err)

		return types.Entity{}, false
	}

	return toEntity(found), true
}

// toEntity maps a todo to its Cedar entity, exposing only the domain's EXISTING
// fields as attributes (no owner field is invented). An attribute policy can
// then decide on a todo's title, status, or timestamps; the shipped coarse
// policy reads none of them, so this mapping runs only when such a policy is
// added. CompletedAt is omitted while the todo is open (a nil pointer), so a
// policy must guard it with `resource has completedAt`.
func toEntity(t todo.Todo) types.Entity {
	attrs := types.RecordMap{
		"id":        types.String(t.ID),
		"title":     types.String(t.Title),
		"status":    types.String(t.Status),
		"createdAt": types.NewDatetime(t.CreatedAt),
	}
	if t.CompletedAt != nil {
		attrs["completedAt"] = types.NewDatetime(*t.CompletedAt)
	}

	return types.Entity{
		UID:        types.NewEntityUID(TodoType, types.String(t.ID)),
		Attributes: types.NewRecord(attrs),
	}
}
