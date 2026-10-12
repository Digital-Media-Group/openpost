package notifications

import (
	"context"
	"testing"

	"github.com/openpost/backend/internal/models"
	"github.com/stretchr/testify/require"
)

func TestApprovalOutcomesRequireTheirIdentifyingFacts(t *testing.T) {
	_, err := NewApprovalRequestedOutcome(ApprovalRequestedFacts{
		RecipientUserID: "", WorkspaceID: "workspace-1", RunID: "run-1", StepID: "step-1",
	})
	require.ErrorIs(t, err, ErrInvalidOutcome)

	_, err = NewApprovalRequestedOutcome(ApprovalRequestedFacts{
		RecipientUserID: "user-1", WorkspaceID: "workspace-1", RunID: "", StepID: "step-1",
	})
	require.ErrorIs(t, err, ErrInvalidOutcome)

	_, err = NewApprovalExpiredOutcome(ApprovalExpiredFacts{
		RecipientUserID: "user-1", WorkspaceID: "workspace-1", RunID: "",
	})
	require.ErrorIs(t, err, ErrInvalidOutcome)
}

// The two topics are deliberately distinct: a wait that starts and a wait that
// dies are different news, and only the second one also tells the author.
func TestApprovalOutcomesUseDistinctTopicsAndIdentities(t *testing.T) {
	requested, err := NewApprovalRequestedOutcome(ApprovalRequestedFacts{
		RecipientUserID: "user-1", WorkspaceID: "workspace-1",
		RunID: "run-1", StepID: "step-1", PublicationID: "publication-1", WorkflowName: "Weekly",
	})
	require.NoError(t, err)
	expired, err := NewApprovalExpiredOutcome(ApprovalExpiredFacts{
		RecipientUserID: "user-1", WorkspaceID: "workspace-1",
		RunID: "run-1", PublicationID: "publication-1", WorkflowName: "Weekly",
	})
	require.NoError(t, err)

	requestedValue := requested.notificationOutcome()
	expiredValue := expired.notificationOutcome()
	require.Equal(t, TypeApprovalRequested, requestedValue.topic)
	require.Equal(t, TypeApprovalExpired, expiredValue.topic)

	// One run can park more than once, so the request is identified by its step
	// as well. The expiry is per run, so it can only ever be reported once.
	require.Equal(t, "run-1:step-1", requestedValue.eventID)
	require.Equal(t, "run-1", expiredValue.eventID)

	input, err := materializeApprovalRequestedOutcome(requestedValue)
	require.NoError(t, err)
	require.Equal(t, "/workflows?run=run-1", input.Href)
	require.Equal(t, "approval_requested:run-1:step-1", input.DedupKey)
	require.NotEmpty(t, input.Title)
	require.NotEmpty(t, input.Actions)

	expiredInput, err := materializeApprovalExpiredOutcome(expiredValue)
	require.NoError(t, err)
	require.Equal(t, "/workflows?run=run-1", expiredInput.Href)
	require.Equal(t, "approval_expired:run-1", expiredInput.DedupKey)
	require.NotEqual(t, input.Title, expiredInput.Title)
}

// Both topics join the publishing group, so they inherit the existing
// preference surface instead of inventing a new one.
func TestApprovalTopicsJoinThePublishingGroup(t *testing.T) {
	for _, id := range []string{TypeApprovalRequested, TypeApprovalExpired} {
		policy, ok := topicPolicyFor(id)
		require.True(t, ok, id)
		require.Equal(t, "publishing", policy.definition.Group)
		require.False(t, policy.definition.Transactional)
		require.True(t, policy.definition.EmailMutable)
		require.Contains(t, policy.definition.EmailFrequencies, EmailFrequencyImmediate)
		require.Equal(t, "notifications.event."+id, policy.definition.PresentationKey)
	}
}

// A missing descriptive detail must never fail the transition reporting it: an
// unnamed workflow or an approval step without a publication is still a real
// wait, and dropping the notification would recreate the silent queue.
func TestApprovalOutcomesTolerateMissingDescriptiveFacts(t *testing.T) {
	requested, err := NewApprovalRequestedOutcome(ApprovalRequestedFacts{
		RecipientUserID: "user-1", WorkspaceID: "workspace-1", RunID: "run-1", StepID: "step-1",
	})
	require.NoError(t, err)
	requestedValue := requested.notificationOutcome()
	require.NotContains(t, requestedValue.payload, "publication_id")
	require.NotContains(t, requestedValue.payload, "workflow_name")
	_, err = materializeApprovalRequestedOutcome(requestedValue)
	require.NoError(t, err)

	expired, err := NewApprovalExpiredOutcome(ApprovalExpiredFacts{
		RecipientUserID: "user-1", WorkspaceID: "workspace-1", RunID: "run-1",
	})
	require.NoError(t, err)
	expiredValue := expired.notificationOutcome()
	require.NotContains(t, expiredValue.payload, "publication_id")
	require.NotContains(t, expiredValue.payload, "workflow_name")
	_, err = materializeApprovalExpiredOutcome(expiredValue)
	require.NoError(t, err)
}

// A parked draft must reach the inbox even at default preferences: the whole
// point is that the queue stops being invisible.
func TestApprovalRequestedReachesTheInboxByDefault(t *testing.T) {
	ctx := context.Background()
	db := notificationsTestDB(t)
	service := NewService(db)

	outcome, err := NewApprovalRequestedOutcome(ApprovalRequestedFacts{
		RecipientUserID: "user-1", WorkspaceID: "workspace-1",
		RunID: "run-1", StepID: "step-1", PublicationID: "publication-1",
		WorkflowName: "Weekly review",
	})
	require.NoError(t, err)
	require.NoError(t, service.Record(ctx, outcome))

	var stored models.UserNotification
	require.NoError(t, db.NewSelect().Model(&stored).
		Where("user_id = ? AND workspace_id = ?", "user-1", "workspace-1").Scan(ctx))
	require.Equal(t, TypeApprovalRequested, stored.Type)
	require.Equal(t, "/workflows?run=run-1", stored.Href)

	// Recording the same park twice must not duplicate the notification.
	require.NoError(t, service.Record(ctx, outcome))
	count, err := db.NewSelect().Model((*models.UserNotification)(nil)).
		Where("user_id = ? AND dedup_key = ?", "user-1", "approval_requested:run-1:step-1").Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
