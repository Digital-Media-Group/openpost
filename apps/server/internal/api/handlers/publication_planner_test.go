package handlers

import (
	"testing"
	"time"

	"github.com/openpost/backend/internal/services/publicationplanner"
	"github.com/stretchr/testify/require"
)

// TestLandedPublicationsKeepsEveryCreatedDraft pins the shape the client reads.
// A partial landing answers 200 with these, because an error response carries no
// body and would hide the drafts that now exist.
func TestLandedPublicationsKeepsEveryCreatedDraft(t *testing.T) {
	result := publicationplanner.LandResult{
		Publications: []publicationplanner.LandedPublication{
			{SlotIndex: 1, PublicationID: "pub-1", ScheduledAt: "2026-03-02T09:00:00Z", Topic: "One"},
			{SlotIndex: 2, PublicationID: "pub-2", ScheduledAt: "2026-03-04T09:00:00Z", Topic: "Two"},
		},
	}

	landed := landedPublications(result)
	require.Len(t, landed, 2)
	require.Equal(t, 1, landed[0].SlotIndex)
	require.Equal(t, "pub-1", landed[0].PublicationID)
	require.Equal(t, "2026-03-02T09:00:00Z", landed[0].ScheduledAt)
	require.Equal(t, "One", landed[0].Topic)
	require.Equal(t, "pub-2", landed[1].PublicationID)
}

// TestLandedPublicationsReportsNothingForAnEmptyResult keeps the empty case from
// inventing an entry.
func TestLandedPublicationsReportsNothingForAnEmptyResult(t *testing.T) {
	require.Empty(t, landedPublications(publicationplanner.LandResult{}))
}

// TestPublicationPlanResponseRendersEverySlot checks the plan the client renders
// carries the server-computed times unchanged.
func TestPublicationPlanResponseRendersEverySlot(t *testing.T) {
	plan := publicationplanner.Plan{
		Model: "test/model",
		Slots: []publicationplanner.Slot{
			{Index: 1, ScheduledAt: mustParseRFC3339(t, "2026-03-02T09:00:00Z"), Topic: "One", Angle: "a", Text: "t"},
			{Index: 2, ScheduledAt: mustParseRFC3339(t, "2026-03-04T09:00:00Z"), Topic: "Two", Angle: "a", Text: "t"},
		},
	}

	response := publicationPlanResponse(plan)
	require.Equal(t, "test/model", response.Model)
	require.Len(t, response.Slots, 2)
	require.Equal(t, "2026-03-02T09:00:00Z", response.Slots[0].ScheduledAt)
	require.Equal(t, "2026-03-04T09:00:00Z", response.Slots[1].ScheduledAt)
	require.Equal(t, 1, response.Slots[0].Index)
	require.Equal(t, "One", response.Slots[0].Topic)
}

// TestPublicationPlanFromRequestRejectsAMalformedPlan keeps untrusted plan data
// from reaching the publication write path.
func TestPublicationPlanFromRequestRejectsAMalformedPlan(t *testing.T) {
	_, err := publicationPlanFromRequest(nil)
	require.Error(t, err)

	_, err = publicationPlanFromRequest([]PublicationPlanSlot{
		{Index: 1, ScheduledAt: "not-a-timestamp", Topic: "One"},
	})
	require.Error(t, err)

	plan, err := publicationPlanFromRequest([]PublicationPlanSlot{
		{Index: 1, ScheduledAt: "2026-03-02T09:00:00Z", Topic: "  One  ", Angle: " a ", Text: " t "},
	})
	require.NoError(t, err)
	require.Len(t, plan.Slots, 1)
	require.Equal(t, "One", plan.Slots[0].Topic, "fields are trimmed")
	require.Equal(t, "a", plan.Slots[0].Angle)
	require.Equal(t, "t", plan.Slots[0].Text)
}

// TestPublicationPlanErrorMapsInputAndModelFailures keeps a caller's mistake from
// being reported as a server fault, and a model fault from being reported as the
// caller's.
func TestPublicationPlanErrorMapsInputAndModelFailures(t *testing.T) {
	require.Nil(t, publicationPlanError(nil))

	input := publicationPlanError(publicationplanner.ErrInvalidInput)
	require.Equal(t, 400, plannerStatusOf(t, input))

	rejected := publicationPlanError(publicationplanner.ErrPlanRejected)
	require.Equal(t, 502, plannerStatusOf(t, rejected))

	unavailable := publicationPlanError(errFakeProviderFailure{})
	require.Equal(t, 503, plannerStatusOf(t, unavailable))
}

type errFakeProviderFailure struct{}

func (errFakeProviderFailure) Error() string { return "provider unreachable" }

func plannerStatusOf(t *testing.T, err error) int {
	t.Helper()
	var statusErr interface{ GetStatus() int }
	require.ErrorAs(t, err, &statusErr)
	return statusErr.GetStatus()
}

func mustParseRFC3339(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}
