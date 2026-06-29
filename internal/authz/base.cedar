// base.cedar holds the cross-cutting policies that belong to no single domain
// slice: the shared principal-role rules over the shared entity namespace.
// Slices contribute their own resource policies; these merge with the base set
// into one runtime PolicySet (see authz.New).

// An admin role may do anything. Membership is carried on the principal's
// claims and projected onto the principal entity's parents at resolve time, so
// this evaluates with no entity load. This is the coarse, default reference
// rule; finer attribute/relationship policies live in each slice.
permit (
    principal in Role::"admin",
    action,
    resource
);
