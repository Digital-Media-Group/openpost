package workflows

import (
	"context"
	"testing"
	"time"

	"github.com/openpost/backend/internal/models"
	"github.com/openpost/backend/internal/services/notifications"
	"github.com/stretchr/testify/require"
)

// approvalNotificationService wires a real notification service into a workflow
// service so the transitions can be observed through the notifications table.
func approvalNotificationService(t *testing.T, s *Service) *notifications.Service {
	t.Helper()
	service := notifications.NewService(s.db)
	s.SetNotificationService(service)
	return service
}

// addApprover inserts an active administrator who is not the run's author, which
// is the audience an approval wait is actually for.
func addApprover(t *testing.T, s *Service, userID string) {
	t.Helper()
	now := time.Now().UTC()
	_, err := s.db.NewInsert().Model(&models.User{ID: userID, Email: userID + "@example.com", PasswordHash: "hash", CreatedAt: now}).Exec(t.Context())
	require.NoError(t, err)
	_, err = s.db.NewInsert().Model(&models.WorkspaceMember{WorkspaceID: "ws", UserID: userID, Role: models.WorkspaceRoleAdmin, Status: models.WorkspaceMemberStatusActive, CreatedAt: now}).Exec(t.Context())
	require.NoError(t, err)
}

func approvalSteps() []Step {
	return []Step{
		{ID: "review", Kind: KindApproval, Inputs: map[string]Value{"publication_id": literal("post-1")}},
		{ID: "outcomes", Kind: KindFields, Inputs: map[string]Value{"fields": reference("review.publication_id")}},
	}
}

func approvalAction() actionFunc {
	return func(_ context.Context, input EffectRequest) (EffectResult, error) {
		if input.Kind == KindApproval {
			return EffectResult{Output: map[string]any{"publication_id": "post-1", "revision": 1, "text": "review me"}}, nil
		}
		return EffectResult{Output: map[string]any{"ok": true}}, nil
	}
}

func notificationsFor(t *testing.T, s *Service, userID, topic string) []models.UserNotification {
	t.Helper()
	rows := []models.UserNotification{}
	require.NoError(t, s.db.NewSelect().Model(&rows).
		Where("user_id = ? AND type = ?", userID, topic).
		Order("created_at ASC").Scan(t.Context()))
	return rows
}

// Parking a draft is the moment the queue becomes someone else's problem. Until
// now it told nobody, which is the whole reason the approval step got worked
// around by hand.
func TestParkedApprovalNotifiesTheOtherAdministrators(t *testing.T) {
	s, actor := workflowTestService(t, approvalAction())
	approvalNotificationService(t, s)
	addApprover(t, s, "approver")

	workflow := saveTestWorkflow(t, s, actor, approvalSteps())
	run, err := s.Start(t.Context(), actor, "ws", workflow.ID, ModeLive, map[string]any{}, workflow.Revision)
	require.NoError(t, err)
	runJob(t, s, run.ID)

	awaiting, err := s.GetRun(t.Context(), actor, "ws", run.ID)
	require.NoError(t, err)
	require.Equal(t, StateApproval, awaiting.State)

	// The approver is told, with a link straight to the run that is waiting.
	approver := notificationsFor(t, s, "approver", notifications.TypeApprovalRequested)
	require.Len(t, approver, 1)
	require.Equal(t, "/workflows?run="+run.ID, approver[0].Href)
	require.Equal(t, "ws", approver[0].WorkspaceID)

	// The author is not told: they just parked it, and the wait is for somebody
	// else.
	require.Empty(t, notificationsFor(t, s, "user", notifications.TypeApprovalRequested))
}

// A workspace whose only administrator is the author would otherwise park a
// draft and tell nobody at all — exactly the silent queue this removes.
func TestParkedApprovalFallsBackToTheAuthorWhenNobodyElseCanApprove(t *testing.T) {
	s, actor := workflowTestService(t, approvalAction())
	approvalNotificationService(t, s)

	workflow := saveTestWorkflow(t, s, actor, approvalSteps())
	run, err := s.Start(t.Context(), actor, "ws", workflow.ID, ModeLive, map[string]any{}, workflow.Revision)
	require.NoError(t, err)
	runJob(t, s, run.ID)

	author := notificationsFor(t, s, "user", notifications.TypeApprovalRequested)
	require.Len(t, author, 1)
	require.Equal(t, "/workflows?run="+run.ID, author[0].Href)
}

// Re-running the same step must not pile up duplicate notifications.
func TestParkedApprovalNotifiesOncePerWait(t *testing.T) {
	s, actor := workflowTestService(t, approvalAction())
	approvalNotificationService(t, s)
	addApprover(t, s, "approver")

	workflow := saveTestWorkflow(t, s, actor, approvalSteps())
	run, err := s.Start(t.Context(), actor, "ws", workflow.ID, ModeLive, map[string]any{}, workflow.Revision)
	require.NoError(t, err)
	runJob(t, s, run.ID)
	runJob(t, s, run.ID)

	require.Len(t, notificationsFor(t, s, "approver", notifications.TypeApprovalRequested), 1)
}

// An expiry is final and used to be silent. Both the approvers and the author
// are told, because the author's post just died.
func TestExpiredApprovalNotifiesApproversAndAuthor(t *testing.T) {
	s, actor := workflowTestService(t, approvalAction())
	approvalNotificationService(t, s)
	addApprover(t, s, "approver")

	workflow := saveTestWorkflow(t, s, actor, approvalSteps())
	run, err := s.Start(t.Context(), actor, "ws", workflow.ID, ModeLive, map[string]any{}, workflow.Revision)
	require.NoError(t, err)
	runJob(t, s, run.ID)

	// Age the wait past MaxRunAge, then run the sweep that fails stale waits.
	_, err = s.db.NewUpdate().Model((*runRecord)(nil)).
		Set("created_at = ?", time.Now().UTC().Add(-MaxRunAge-time.Hour)).
		Where("id = ?", run.ID).Exec(t.Context())
	require.NoError(t, err)
	require.NoError(t, s.sweep(context.Background()))

	var stored runRecord
	require.NoError(t, s.db.NewSelect().Model(&stored).Where("id = ?", run.ID).Scan(t.Context()))
	require.Equal(t, StateFailed, stored.State)

	for _, userID := range []string{"approver", "user"} {
		rows := notificationsFor(t, s, userID, notifications.TypeApprovalExpired)
		require.Len(t, rows, 1, userID)
		require.Equal(t, "/workflows?run="+run.ID, rows[0].Href)
	}

	// A second sweep must not re-report an expiry that was already recorded.
	require.NoError(t, s.sweep(context.Background()))
	for _, userID := range []string{"approver", "user"} {
		require.Len(t, notificationsFor(t, s, userID, notifications.TypeApprovalExpired), 1, userID)
	}
}

// A workflow service without a notification service must keep running. The
// wiring is optional so existing constructions stay valid.
func TestApprovalTransitionsStaySilentWithoutANotificationService(t *testing.T) {
	s, actor := workflowTestService(t, approvalAction())

	workflow := saveTestWorkflow(t, s, actor, approvalSteps())
	run, err := s.Start(t.Context(), actor, "ws", workflow.ID, ModeLive, map[string]any{}, workflow.Revision)
	require.NoError(t, err)
	runJob(t, s, run.ID)

	awaiting, err := s.GetRun(t.Context(), actor, "ws", run.ID)
	require.NoError(t, err)
	require.Equal(t, StateApproval, awaiting.State)
}
