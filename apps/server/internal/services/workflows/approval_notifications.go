package workflows

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/openpost/backend/internal/models"
	"github.com/openpost/backend/internal/services/notifications"
	"github.com/openpost/backend/internal/services/workspaceaccess"
	"github.com/uptrace/bun"
)

// approvalExpiredMessage is the run error recorded when a wait runs out. It is
// unchanged from the silent version of this sweep.
const approvalExpiredMessage = "The approval expired after 30 days."

// Approval transitions are the one place a workflow asks someone other than its
// author to act, and they used to be silent. These helpers turn the two
// transitions into notifications recorded in the same transaction that commits
// the run state, so a notification can never describe a state that did not
// happen and a parked run can never be invisible.

// approvalRecipients resolves who may decide a parked approval: the workspace's
// active members whose role satisfies LevelApprove, excluding excludeUserID.
//
// The audience is derived from the access package rather than written out here.
// Approving is LevelApprove, and the roles that satisfy it are not only the
// administrators — a client may decide an approval without being able to
// administer the workspace, which is the entire point of that role. A hardcoded
// administrator filter would leave the client out of the audience, so the
// person the run is waiting for would be the one person never told about it.
func (s *Service) approvalRecipients(ctx context.Context, db bun.IDB, workspaceID, excludeUserID string) ([]string, error) {
	roles := workspaceaccess.RolesMeetingLevel(workspaceaccess.LevelApprove)
	if len(roles) == 0 {
		return nil, nil
	}
	members := []models.WorkspaceMember{}
	if err := db.NewSelect().Model(&members).
		Column("user_id").
		Where("workspace_id = ? AND status = ?", workspaceID, models.WorkspaceMemberStatusActive).
		Where("role IN (?)", bun.List(roles)).
		Order("created_at ASC", "user_id ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	recipients := make([]string, 0, len(members))
	for _, member := range members {
		userID := strings.TrimSpace(member.UserID)
		if userID == "" || userID == excludeUserID {
			continue
		}
		recipients = append(recipients, userID)
	}
	return recipients, nil
}

// runAuthor reports who started the run, as recorded in its stored authority.
func runAuthor(record runRecord) string {
	var authority workspaceaccess.StoredAuthority
	if err := json.Unmarshal([]byte(record.AuthorityJSON), &authority); err != nil {
		return ""
	}
	return strings.TrimSpace(authority.UserID)
}

// parkedApproval finds the step result that put the run into the approval
// state. It is how the publication being reviewed is recovered, both when the
// wait starts and when it expires much later.
func parkedApproval(results []StepResult) (StepResult, bool) {
	for index := len(results) - 1; index >= 0; index-- {
		if results[index].State == StateApproval {
			return results[index], true
		}
	}
	return StepResult{}, false
}

func decodeRunResults(record runRecord) []StepResult {
	results := []StepResult{}
	if err := json.Unmarshal([]byte(record.ResultsJSON), &results); err != nil {
		return nil
	}
	return results
}

func approvalPublicationID(result StepResult) string {
	publicationID, _ := result.Inputs["publication_id"].(string)
	return strings.TrimSpace(publicationID)
}

// recordApprovalRequested notifies the approvers that a run has parked. The
// author is skipped: they just took the action that parked it, and the wait is
// for somebody else. If that leaves nobody — a workspace whose only approver is
// the author — the author is told after all, because a silent queue is the
// exact problem this exists to remove.
func (s *Service) recordApprovalRequested(ctx context.Context, db bun.IDB, record runRecord, results []StepResult) error {
	if s.notifications == nil {
		return nil
	}
	result, ok := parkedApproval(results)
	if !ok {
		return nil
	}
	author := runAuthor(record)
	recipients, err := s.approvalRecipients(ctx, db, record.WorkspaceID, author)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		if author == "" {
			return nil
		}
		recipients = []string{author}
	}
	for _, userID := range recipients {
		outcome, err := notifications.NewApprovalRequestedOutcome(notifications.ApprovalRequestedFacts{
			RecipientUserID: userID,
			WorkspaceID:     record.WorkspaceID,
			RunID:           record.ID,
			StepID:          result.StepID,
			PublicationID:   approvalPublicationID(result),
			WorkflowName:    record.WorkflowName,
		})
		if err != nil {
			return err
		}
		if err := s.notifications.RecordWithDB(ctx, db, outcome); err != nil {
			return err
		}
	}
	return nil
}

// expireApprovalWait fails one wait that ran out and tells the people who were
// waiting on it. The state change and its notifications commit together, so an
// expired wait is never both silent and final. It reports whether the wait was
// actually expired, so the caller can record telemetry only for a change that
// really committed.
func (s *Service) expireApprovalWait(ctx context.Context, record runRecord, now time.Time) (bool, error) {
	expired := false
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		updated, err := tx.NewUpdate().Model((*runRecord)(nil)).
			Set("state = ?, error = ?, revision = revision + 1, updated_at = ?", StateFailed, approvalExpiredMessage, now).
			Where("id = ? AND state = ?", record.ID, StateApproval).Exec(ctx)
		if err != nil {
			return err
		}
		if affected, _ := updated.RowsAffected(); affected != 1 {
			return nil
		}
		expired = true
		return s.recordApprovalExpired(ctx, tx, record)
	})
	return expired, err
}

// recordApprovalExpired notifies the approvers and the author that a wait ran
// out. Unlike the request, the author is included: their post died, and today
// the draft simply disappears from the queue with nothing said.
func (s *Service) recordApprovalExpired(ctx context.Context, db bun.IDB, record runRecord) error {
	if s.notifications == nil {
		return nil
	}
	result, ok := parkedApproval(decodeRunResults(record))
	if !ok {
		return nil
	}
	author := runAuthor(record)
	recipients, err := s.approvalRecipients(ctx, db, record.WorkspaceID, "")
	if err != nil {
		return err
	}
	if author != "" {
		already := false
		for _, userID := range recipients {
			if userID == author {
				already = true
				break
			}
		}
		if !already {
			recipients = append(recipients, author)
		}
	}
	for _, userID := range recipients {
		outcome, err := notifications.NewApprovalExpiredOutcome(notifications.ApprovalExpiredFacts{
			RecipientUserID: userID,
			WorkspaceID:     record.WorkspaceID,
			RunID:           record.ID,
			PublicationID:   approvalPublicationID(result),
			WorkflowName:    record.WorkflowName,
		})
		if err != nil {
			return err
		}
		if err := s.notifications.RecordWithDB(ctx, db, outcome); err != nil {
			return err
		}
	}
	return nil
}
