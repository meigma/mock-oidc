package authz

import (
	"context"
	"net/http"
	"testing"

	"github.com/cedar-policy/cedar-go/types"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireRecordsActionAndIDParam(t *testing.T) {
	t.Parallel()

	action := types.NewEntityUID("Action", "todo:read")
	meta := Require(action, "todoID")

	decl, ok := meta[metadataKey].(*declaration)
	require.True(t, ok, "metadata must carry a declaration")
	assert.Equal(t, kindRequire, decl.kind)
	assert.Equal(t, action, decl.action)
	assert.Equal(t, "todoID", decl.idParam)
}

func TestRequireWithoutIDParam(t *testing.T) {
	t.Parallel()

	decl, ok := Require(types.NewEntityUID("Action", "todo:list"))[metadataKey].(*declaration)
	require.True(t, ok)
	assert.Empty(t, decl.idParam, "a collection operation binds no instance id")
}

func TestPublicRecordsOptOut(t *testing.T) {
	t.Parallel()

	meta := Public()
	decl, ok := meta[metadataKey].(*declaration)
	require.True(t, ok)
	assert.Equal(t, kindPublic, decl.kind)
	assert.NotContains(t, meta, "security", "a public operation declares no security requirement")
}

func TestDeclarationFrom(t *testing.T) {
	t.Parallel()

	t.Run("present", func(t *testing.T) {
		t.Parallel()

		op := &huma.Operation{Metadata: Public()}
		decl, ok := declarationFrom(op)
		require.True(t, ok)
		assert.Equal(t, kindPublic, decl.kind)
	})

	t.Run("undeclared operation", func(t *testing.T) {
		t.Parallel()

		_, ok := declarationFrom(&huma.Operation{})
		assert.False(t, ok, "an operation with no declaration must be reported as undeclared")
	})

	t.Run("nil operation", func(t *testing.T) {
		t.Parallel()

		_, ok := declarationFrom(nil)
		assert.False(t, ok)
	})
}

func TestApplySecurityPopulatesRequiredOperations(t *testing.T) {
	t.Parallel()

	_, api := humatest.New(t)

	noop := func(_ context.Context, _ *struct{}) (*struct{}, error) { return &struct{}{}, nil }
	huma.Register(api, huma.Operation{
		OperationID: "protected",
		Method:      http.MethodGet,
		Path:        "/protected",
		Metadata:    Require(types.NewEntityUID("Action", "todo:read")),
	}, noop)
	huma.Register(api, huma.Operation{
		OperationID: "public",
		Method:      http.MethodGet,
		Path:        "/public",
		Metadata:    Public(),
	}, noop)

	ApplySecurity(api)

	protected := api.OpenAPI().Paths["/protected"].Get
	assert.Equal(t, SecurityRequirement(), protected.Security,
		"ApplySecurity must stamp the requirement onto a Require operation")

	public := api.OpenAPI().Paths["/public"].Get
	assert.Empty(t, public.Security, "a public operation advertises no security requirement")
}

func TestApplySecuritySurfacesInGeneratedSpec(t *testing.T) {
	t.Parallel()

	_, api := humatest.New(t)
	RegisterSecurityScheme(api)

	huma.Register(api, huma.Operation{
		OperationID: "protected",
		Method:      http.MethodGet,
		Path:        "/protected",
		Metadata:    Require(types.NewEntityUID("Action", "todo:read")),
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) { return &struct{}{}, nil })

	ApplySecurity(api)

	spec, err := api.OpenAPI().YAML()
	require.NoError(t, err)
	assert.Contains(t, string(spec), SecuritySchemeName,
		"the protected operation's security requirement must appear in the generated spec")
}

func TestResourceTypeFromAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action types.EntityUID
		want   types.EntityType
	}{
		{
			name:   "resource verb maps to pascal-cased type",
			action: types.NewEntityUID("Action", "todo:read"),
			want:   "Todo",
		},
		{
			name:   "multi-segment verb keeps only the resource",
			action: types.NewEntityUID("Action", "todo:list:all"),
			want:   "Todo",
		},
		{
			name:   "action without a resource prefix yields a zero type",
			action: types.NewEntityUID("Action", "ping"),
			want:   "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, resourceTypeFromAction(tc.action).Type)
		})
	}
}
