// Package authz is the todo resource's authorization slice. It contributes the
// todo-specific Cedar policies, the typed action identifiers the HTTP registrar
// tags its operations with, and a repository-backed entity ("fact") resolver
// that maps a todo into a Cedar entity on demand. The composition root collects
// this slice's Contribution alongside every other slice's and merges them into
// the one runtime engine (see internal/authz).
//
// The dependency runs slice -> domain core only: this package imports the todo
// domain and Cedar, while the todo domain never imports this package, so the
// Cedar-free-domain rule holds. The package is named authz to mirror the like
// per-domain package naming (todo/httpapi, todo/postgres); files needing both
// this slice and the base engine alias one on import (todoauthz).
package authz

import "github.com/cedar-policy/cedar-go/types"

// actionType is the Cedar entity type of every action, by convention.
const actionType types.EntityType = "Action"

// The todo actions, one per operation the HTTP slice exposes. Each is a Cedar
// action UID of the form Action::"<resource>:<verb>" (the naming convention in
// the design): the "<resource>" segment (todo) lets the base engine derive the
// type-level resource (Todo) for a collection operation, and the verb names the
// operation. The HTTP registrar tags each route with the matching identifier via
// authz.Require, and policy.cedar grants them to authenticated principals.
//
// These are package-level vars rather than consts because a Cedar EntityUID is a
// struct, which Go cannot express as a const; they are effectively immutable
// typed identifiers (never reassigned).
//
//nolint:gochecknoglobals // immutable typed Cedar action identifiers; EntityUID is a struct and cannot be const.
var (
	// ActionCreate authorizes creating a todo.
	ActionCreate = types.NewEntityUID(actionType, "todo:create")
	// ActionRead authorizes reading a single todo.
	ActionRead = types.NewEntityUID(actionType, "todo:read")
	// ActionUpdate authorizes updating a todo (for example, completing it).
	ActionUpdate = types.NewEntityUID(actionType, "todo:update")
	// ActionList authorizes listing todos.
	ActionList = types.NewEntityUID(actionType, "todo:list")
)

// actions lists every action this slice declares, recorded on the Contribution
// for discovery and validation.
func actions() []types.EntityUID {
	return []types.EntityUID{ActionCreate, ActionRead, ActionUpdate, ActionList}
}
