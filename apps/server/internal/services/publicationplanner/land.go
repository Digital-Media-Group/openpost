package publicationplanner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openpost/backend/internal/models"
	publicationservice "github.com/openpost/backend/internal/services/publications"
)

// PublicationCreator is the narrow canonical seam this package needs to hand a
// planned slot to the normal composer. It is the same boundary the publication
// builder uses, so a plan lands through the one Publication write path rather
// than a second one.
type PublicationCreator interface {
	Create(context.Context, string, publicationservice.CreateCommand) (publicationservice.Publication, error)
}

// LandedPublication identifies one draft a plan produced.
type LandedPublication struct {
	SlotIndex     int
	PublicationID string
	ScheduledAt   string
	Topic         string
}

// LandResult reports what a plan became.
type LandResult struct {
	Publications []LandedPublication
}

// Land creates one draft Publication per slot.
//
// Every Publication is created as a draft with the slot's planned time stored on
// it. A draft is never picked up for delivery: publishing requires the
// scheduled state, which only an explicit scheduling command sets. Landing a
// plan therefore cannot publish anything, which is the property that makes
// planning a week safe.
//
// Landing is not atomic across slots. If a later slot fails, the drafts already
// created stay: they are ordinary drafts the author can keep, edit, or delete,
// and deleting a draft a person can see is worse than leaving it. The error
// reports how many landed so the caller can say so plainly.
func (s *Service) Land(
	ctx context.Context,
	userID string,
	request Request,
	plan Plan,
	publications PublicationCreator,
) (LandResult, error) {
	if publications == nil {
		return LandResult{}, fmt.Errorf("%w: publication creator is unavailable", ErrInvalidInput)
	}
	request, err := request.normalize()
	if err != nil {
		return LandResult{}, err
	}
	if len(plan.Slots) == 0 {
		return LandResult{}, ErrNoSlots
	}

	result := LandResult{Publications: make([]LandedPublication, 0, len(plan.Slots))}
	for _, slot := range plan.Slots {
		scheduledAt := slot.ScheduledAt
		created, err := publications.Create(ctx, userID, publicationservice.CreateCommand{
			WorkspaceID:      request.WorkspaceID,
			Title:            slot.Topic,
			CreationPreset:   models.PublishingIntentPost,
			ContentProfile:   models.ContentProfileShortText,
			SourceText:       slot.Text,
			Goal:             slot.Angle,
			ScheduledAt:      &scheduledAt,
			SocialAccountIDs: request.AccountIDs,
		})
		if err != nil {
			return result, fmt.Errorf("create planned draft %d of %d: %w", slot.Index, len(plan.Slots), err)
		}
		result.Publications = append(result.Publications, LandedPublication{
			SlotIndex:     slot.Index,
			PublicationID: strings.TrimSpace(created.ID),
			ScheduledAt:   scheduledAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			Topic:         slot.Topic,
		})
	}
	if len(result.Publications) == 0 {
		return LandResult{}, errors.New("publication plan landed no drafts")
	}
	return result, nil
}
