// policy.cedar holds the todo slice's authorization policies. They merge with
// base.cedar (the admin override) and every other slice's policies into the one
// runtime PolicySet (see internal/authz.New).
//
// Day-one policy: COARSE by design. A principal carrying the "user" role may
// perform any todo action. Membership in Role::"user" is projected onto the
// principal entity's parents from the caller's claims at authentication time, so
// this evaluates with NO entity load — no todo is read to make the decision. The
// demo then exercises the full matrix: a user-role key is allowed, an admin-role
// key is allowed via base.cedar, and a key with neither role is denied (403),
// while no key is denied as anonymous (401).
permit (
    principal in Role::"user",
    action in [
        Action::"todo:create",
        Action::"todo:read",
        Action::"todo:update",
        Action::"todo:list"
    ],
    resource
);

// EXTENSION EXAMPLE (commented — not active): an instance/attribute policy.
//
// The coarse rule above needs no resource attributes, so no todo is ever loaded.
// To authorize on a todo's own attributes instead — for example, "a user may
// read only their own todo" — a policy dereferences resource.<attr>, which is
// the moment Cedar pulls the entity through the fact resolver in facts.go
// (lazily, on demand). The middleware already binds the instance resource
// Todo::"<id>" from the {id} path parameter (Require(action, "id")), so the
// resolver loads exactly that todo and maps its fields to entity attributes.
//
// This template's Todo carries no owner field, so the example is illustrative;
// adding ownership would mean adding the field to the domain and exposing it in
// facts.go. With such a field the policy would read:
//
// permit (
//     principal,
//     action == Action::"todo:read",
//     resource
// ) when {
//     resource.owner == principal
// };
