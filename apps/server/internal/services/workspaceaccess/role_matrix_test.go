package workspaceaccess

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/openpost/backend/internal/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "github.com/uptrace/bun/driver/sqliteshim"
)

// TestRoleLevelMatrix pins the whole role-to-capability matrix in one place. The
// client role is the reason this test exists: it must be able to connect
// accounts and decide approvals, and it must not be able to author content or
// administer the workspace. A future edit that widens or narrows any cell has
// to change this table on purpose.
func TestRoleLevelMatrix(t *testing.T) {
	cases := []struct {
		role    string
		level   Level
		allowed bool
	}{
		// Admin keeps everything.
		{models.WorkspaceRoleAdmin, LevelRead, true},
		{models.WorkspaceRoleAdmin, LevelConnect, true},
		{models.WorkspaceRoleAdmin, LevelApprove, true},
		{models.WorkspaceRoleAdmin, LevelEdit, true},
		{models.WorkspaceRoleAdmin, LevelAdminister, true},

		// Editor authors content but does not administer the workspace.
		{models.WorkspaceRoleEditor, LevelRead, true},
		{models.WorkspaceRoleEditor, LevelConnect, true},
		{models.WorkspaceRoleEditor, LevelApprove, false},
		{models.WorkspaceRoleEditor, LevelEdit, true},
		{models.WorkspaceRoleEditor, LevelAdminister, false},

		// Client connects accounts and decides approvals, and nothing else.
		{models.WorkspaceRoleClient, LevelRead, true},
		{models.WorkspaceRoleClient, LevelConnect, true},
		{models.WorkspaceRoleClient, LevelApprove, true},
		{models.WorkspaceRoleClient, LevelEdit, false},
		{models.WorkspaceRoleClient, LevelAdminister, false},

		// Viewer only reads.
		{models.WorkspaceRoleViewer, LevelRead, true},
		{models.WorkspaceRoleViewer, LevelConnect, false},
		{models.WorkspaceRoleViewer, LevelApprove, false},
		{models.WorkspaceRoleViewer, LevelEdit, false},
		{models.WorkspaceRoleViewer, LevelAdminister, false},

		// An unknown role is denied everywhere, including read.
		{"owner", LevelRead, false},
		{"owner", LevelConnect, false},
		{"owner", LevelApprove, false},
		{"", LevelRead, false},
	}

	for _, tc := range cases {
		t.Run(tc.role+"_"+string(tc.level), func(t *testing.T) {
			require.Equal(t, tc.allowed, RoleMeetsLevel(tc.role, tc.level))
		})
	}
}

// TestRolesMeetingLevelIsTheInverseOfTheMatrix keeps the derived audience
// honest: whoever is told they may decide an approval must be exactly who may
// decide it. The two must never drift, because a caller that addresses the
// approvers is relying on this answer to reach a real person.
func TestRolesMeetingLevelIsTheInverseOfTheMatrix(t *testing.T) {
	for _, role := range knownRoles {
		for _, level := range []Level{LevelRead, LevelConnect, LevelApprove, LevelEdit, LevelAdminister} {
			meets := RoleMeetsLevel(role, level)
			listed := slices.Contains(RolesMeetingLevel(level), role)
			require.Equal(t, meets, listed, "role %s at level %s", role, level)
		}
	}

	// Approving is the case that motivated this: a client decides approvals
	// without administering the workspace, so an audience of administrators
	// alone would omit the person the wait is for.
	require.Equal(t,
		[]string{models.WorkspaceRoleAdmin, models.WorkspaceRoleClient},
		RolesMeetingLevel(LevelApprove))
	require.Equal(t,
		[]string{models.WorkspaceRoleAdmin, models.WorkspaceRoleEditor, models.WorkspaceRoleClient, models.WorkspaceRoleViewer},
		RolesMeetingLevel(LevelRead))
	require.Equal(t,
		[]string{models.WorkspaceRoleAdmin},
		RolesMeetingLevel(LevelAdminister))

	// Every known role is accounted for, so a role added to the model without
	// being added to knownRoles cannot be silently invisible here.
	require.Len(t, knownRoles, 4)
}

// TestUnknownLevelDegradesToRead documents the fallback in normalizeLevel
// rather than celebrating it. An unrecognized level degrades to LevelRead,
// which is the most permissive level, so a typo in a level name silently
// weakens a requirement instead of denying it. The guarantee this test pins is
// only that such a level never grants a write capability: it must not reach
// edit, administer, connect, or approve. Any new level added to this package
// has to be registered in normalizeLevel to escape this fallback.
func TestUnknownLevelDegradesToRead(t *testing.T) {
	unknown := Level("publish")

	// It degrades to read, so a reader still reads...
	require.True(t, RoleMeetsLevel(models.WorkspaceRoleViewer, unknown))
	require.True(t, RoleMeetsLevel(models.WorkspaceRoleClient, unknown))

	// ...but it grants nothing beyond read, which is the part that matters.
	require.False(t, RoleMeetsLevel(models.WorkspaceRoleViewer, LevelEdit))
	require.False(t, RoleMeetsLevel(models.WorkspaceRoleViewer, LevelAdminister))
	require.False(t, RoleMeetsLevel(models.WorkspaceRoleViewer, LevelConnect))
	require.False(t, RoleMeetsLevel(models.WorkspaceRoleViewer, LevelApprove))

	// A role that does not exist reads nothing.
	require.False(t, RoleMeetsLevel("", unknown))
	require.False(t, RoleMeetsLevel("owner", unknown))
}

// TestAuthorizeHonorsClientRole drives the same matrix through the real
// authorization path against stored membership, so the capability is proven
// where it is actually enforced and not only in the helper.
func TestAuthorizeHonorsClientRole(t *testing.T) {
	sqlDB, err := sql.Open("sqliteshim", "file:client-role?mode=memory&cache=shared")
	require.NoError(t, err)
	db := bun.NewDB(sqlDB, sqlitedialect.New())
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	ctx := context.Background()
	_, err = db.NewCreateTable().Model((*models.WorkspaceMember)(nil)).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewInsert().Model(&models.WorkspaceMember{
		WorkspaceID: "workspace-1",
		UserID:      "client-1",
		Role:        models.WorkspaceRoleClient,
		Status:      models.WorkspaceMemberStatusActive,
	}).Exec(ctx)
	require.NoError(t, err)

	authorizer := NewAuthorizer(db)
	actor := ActorFacts{UserID: "client-1"}

	for _, level := range []Level{LevelRead, LevelConnect, LevelApprove} {
		decision, err := authorizer.Authorize(ctx, "workspace-1", actor, level)
		require.NoError(t, err)
		require.True(t, decision.Allowed, "client should be allowed at %s", level)
		require.Equal(t, models.WorkspaceRoleClient, decision.Role)
	}

	for _, level := range []Level{LevelEdit, LevelAdminister} {
		decision, err := authorizer.Authorize(ctx, "workspace-1", actor, level)
		require.NoError(t, err)
		require.False(t, decision.Allowed, "client must not be allowed at %s", level)
	}
}
