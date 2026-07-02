package controlapi

import (
	"github.com/danielgtaylor/huma/v2"
)

// tagMockControl groups every control operation in the OpenAPI document. The
// surface is test-only and disabled by --control-enabled=false.
const tagMockControl = "Mock Control"

// mockControlGroupDoc is the group description stamped, in the spec itself, so a
// reader knows the surface is for testing only.
const mockControlGroupDoc = "Test-time control plane (mock-oidc). Direct token mint, one-shot scenario " +
	"enqueue, captured-request inspection, and clock control. Test-only; disabled by --control-enabled=false."

// Register mounts the /_mock control operations onto api. It applies the reserved
// Prefix itself via huma.NewGroup and registers RELATIVE paths on the group, so the
// operations resolve to /_mock/mint, /_mock/scenarios, … with exactly one prefix.
// The composition root passes the BASE huma.API (it must not pre-wrap it in a group,
// or the paths would double to /_mock/_mock). Every operation is tagged
// "Mock Control".
func Register(api huma.API, deps Deps) {
	h := &handlers{deps: deps}
	grp := huma.NewGroup(api, Prefix)

	doc := api.OpenAPI()
	doc.Tags = append(doc.Tags, &huma.Tag{Name: tagMockControl, Description: mockControlGroupDoc})

	h.registerMint(grp)
	h.registerScenarios(grp)
	h.registerRequests(grp)
	h.registerClock(grp)
	h.registerReset(grp)
}
