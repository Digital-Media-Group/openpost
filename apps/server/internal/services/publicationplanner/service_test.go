package publicationplanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openpost/backend/internal/ai"
	publicationservice "github.com/openpost/backend/internal/services/publications"
	"github.com/stretchr/testify/require"
)

// planGenerator is a scripted generator. It records the request it received so a
// test can assert what the model was actually asked, which matters here because
// the schedule must reach the model and the model must not be able to answer
// with dates.
type planGenerator struct {
	response string
	err      error
	requests []ai.GenerateRequest
}

func (g *planGenerator) Generate(_ context.Context, request ai.GenerateRequest) (ai.GenerateResult, error) {
	g.requests = append(g.requests, request)
	if g.err != nil {
		return ai.GenerateResult{}, g.err
	}
	return ai.GenerateResult{Text: g.response, Model: "test/model"}, nil
}

// planJSON renders a well-formed model response with one post per topic.
func planJSON(topics ...string) string {
	posts := make([]map[string]string, 0, len(topics))
	for _, topic := range topics {
		posts = append(posts, map[string]string{
			"topic": topic,
			"angle": "Why " + topic + " matters.",
			"text":  "A draft about " + topic + ".",
		})
	}
	encoded, err := json.Marshal(map[string]any{"posts": posts})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func testRequest() Request {
	return Request{
		WorkspaceID: "ws",
		Brief:       "A week about our new analytics feature.",
		StartDate:   time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC),
		Days:        7,
		Slots:       3,
		Hour:        9,
		Location:    time.UTC,
		AccountIDs:  []string{"x-1"},
	}
}

func TestPlanReturnsOnePostPerScheduledSlot(t *testing.T) {
	generator := &planGenerator{response: planJSON("Analytics launch", "How teams measure", "A customer story")}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	plan, err := service.Plan(context.Background(), testRequest())
	require.NoError(t, err)
	require.Len(t, plan.Slots, 3)
	require.Equal(t, "test/model", plan.Model)

	// The dates come from the schedule, not from the model.
	require.Equal(t, 2, plan.Slots[0].ScheduledAt.Day())
	require.Equal(t, 4, plan.Slots[1].ScheduledAt.Day())
	require.Equal(t, 6, plan.Slots[2].ScheduledAt.Day())
	for index, slot := range plan.Slots {
		require.Equal(t, index+1, slot.Index)
		require.Equal(t, 9, slot.ScheduledAt.Hour())
	}
	require.Equal(t, "Analytics launch", plan.Slots[0].Topic)
}

// TestPlanSendsTheScheduleToTheModel pins the two halves of the division of
// labour: the model must receive the dates and weekdays it is planning around,
// and it must not be asked for a date back.
func TestPlanSendsTheScheduleToTheModel(t *testing.T) {
	generator := &planGenerator{response: planJSON("One", "Two", "Three")}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	_, err = service.Plan(context.Background(), testRequest())
	require.NoError(t, err)
	require.Len(t, generator.requests, 1)

	prompt := generator.requests[0].UserPrompt
	require.Contains(t, prompt, "2026-03-02")
	require.Contains(t, prompt, "2026-03-04")
	require.Contains(t, prompt, "2026-03-06")
	require.Contains(t, prompt, "Monday")
	require.Contains(t, prompt, "Wednesday")
	require.Contains(t, prompt, "A week about our new analytics feature.")

	// The response schema declares no date or time field.
	schema := generator.requests[0].ResponseSchema
	require.NotNil(t, schema)
	properties := schema.Schema["properties"].(map[string]any)
	posts := properties["posts"].(map[string]any)
	item := posts["items"].(map[string]any)
	itemProperties := item["properties"].(map[string]any)
	require.NotContains(t, itemProperties, "scheduled_at")
	require.NotContains(t, itemProperties, "date")
	require.NotContains(t, itemProperties, "time")
	require.Len(t, itemProperties, 3)
}

func TestPlanRejectsAWrongNumberOfPosts(t *testing.T) {
	// The schedule asks for three; the model answers with two.
	generator := &planGenerator{response: planJSON("One", "Two")}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	_, err = service.Plan(context.Background(), testRequest())
	require.ErrorIs(t, err, ErrPlanRejected)
	require.ErrorContains(t, err, errSlotCountMismatch.Error())
}

// TestPlanRejectsADateFromTheModel proves a timestamp in model output cannot
// reach the calendar: the strict decoder refuses the undeclared field outright.
func TestPlanRejectsADateFromTheModel(t *testing.T) {
	response := `{"posts":[
		{"topic":"One","angle":"a","text":"t","scheduled_at":"2026-03-02T09:00:00Z"},
		{"topic":"Two","angle":"a","text":"t"},
		{"topic":"Three","angle":"a","text":"t"}
	]}`
	generator := &planGenerator{response: response}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	_, err = service.Plan(context.Background(), testRequest())
	require.ErrorIs(t, err, ErrPlanRejected)
}

func TestPlanRejectsIncompleteSlots(t *testing.T) {
	for _, tc := range []struct {
		name string
		post string
	}{
		{"empty topic", `{"topic":"","angle":"a","text":"t"}`},
		{"whitespace angle", `{"topic":"t","angle":"   ","text":"t"}`},
		{"empty text", `{"topic":"t","angle":"a","text":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := fmt.Sprintf(`{"posts":[%s,%s,%s]}`, tc.post, tc.post, tc.post)
			generator := &planGenerator{response: response}
			service, err := New(generator, "test/model")
			require.NoError(t, err)

			_, err = service.Plan(context.Background(), testRequest())
			require.ErrorIs(t, err, ErrPlanRejected)
			require.ErrorContains(t, err, "incomplete")
		})
	}
}

func TestPlanToleratesAFencedJSONResponse(t *testing.T) {
	generator := &planGenerator{response: "```json\n" + planJSON("One", "Two", "Three") + "\n```"}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	plan, err := service.Plan(context.Background(), testRequest())
	require.NoError(t, err)
	require.Len(t, plan.Slots, 3)
}

func TestPlanPropagatesGeneratorFailure(t *testing.T) {
	sentinel := errors.New("provider unavailable")
	generator := &planGenerator{err: sentinel}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	_, err = service.Plan(context.Background(), testRequest())
	require.ErrorIs(t, err, sentinel)
}

func TestNewRequiresGeneratorAndModel(t *testing.T) {
	_, err := New(nil, "test/model")
	require.ErrorIs(t, err, ErrInvalidInput)

	_, err = New(&planGenerator{}, "   ")
	require.ErrorIs(t, err, ErrInvalidInput)
}

// TestPlanValidatesTheRequestBeforeSpendingAModelCall keeps a malformed request
// from costing a generation.
func TestPlanValidatesTheRequestBeforeSpendingAModelCall(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"no workspace", func(r *Request) { r.WorkspaceID = "  " }},
		{"no brief", func(r *Request) { r.Brief = "" }},
		{"brief too long", func(r *Request) { r.Brief = strings.Repeat("x", MaxBriefCharacters+1) }},
		{"no destination", func(r *Request) { r.AccountIDs = nil }},
		{"too many destinations", func(r *Request) {
			r.AccountIDs = make([]string, MaxAccountIDs+1)
			for index := range r.AccountIDs {
				r.AccountIDs[index] = fmt.Sprintf("account-%d", index)
			}
		}},
		{"too many slots", func(r *Request) { r.Slots = MaxSlots + 1 }},
		{"zero slots", func(r *Request) { r.Slots = 0 }},
		{"too many days", func(r *Request) { r.Days = MaxDays + 1 }},
		{"more slots than days", func(r *Request) { r.Days = 2; r.Slots = 3 }},
		{"hour out of range", func(r *Request) { r.Hour = 24 }},
		{"no start date", func(r *Request) { r.StartDate = time.Time{} }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			generator := &planGenerator{response: planJSON("One", "Two", "Three")}
			service, err := New(generator, "test/model")
			require.NoError(t, err)

			request := testRequest()
			tc.mutate(&request)

			_, err = service.Plan(context.Background(), request)
			require.ErrorIs(t, err, ErrInvalidInput)
			require.Empty(t, generator.requests, "a rejected request must not reach the model")
		})
	}
}

// TestPlanDefaultsThePeriodToOneWeek documents the default the feature is named
// for: an omitted period means seven days.
func TestPlanDefaultsThePeriodToOneWeek(t *testing.T) {
	generator := &planGenerator{response: planJSON("One", "Two", "Three")}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	request := testRequest()
	request.Days = 0

	normalized, err := request.normalize()
	require.NoError(t, err)
	require.Equal(t, DefaultDays, normalized.Days)

	plan, err := service.Plan(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, plan.Slots, 3)
}

// recordingCreator is a scripted PublicationCreator that records every command
// it was asked to create.
type recordingCreator struct {
	commands []publicationservice.CreateCommand
	failAt   int
	err      error
}

func (c *recordingCreator) Create(_ context.Context, _ string, command publicationservice.CreateCommand) (publicationservice.Publication, error) {
	c.commands = append(c.commands, command)
	if c.failAt > 0 && len(c.commands) == c.failAt {
		return publicationservice.Publication{}, c.err
	}
	return publicationservice.Publication{ID: fmt.Sprintf("publication-%d", len(c.commands))}, nil
}

// TestLandCreatesOneDraftPerSlot is the property that makes planning a week
// safe: every planned post becomes an ordinary draft with its planned time
// stored on it, and nothing is scheduled for delivery.
func TestLandCreatesOneDraftPerSlot(t *testing.T) {
	generator := &planGenerator{response: planJSON("Analytics launch", "How teams measure", "A customer story")}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	request := testRequest()
	plan, err := service.Plan(context.Background(), request)
	require.NoError(t, err)

	creator := &recordingCreator{}
	result, err := service.Land(context.Background(), "user", request, plan, creator)
	require.NoError(t, err)
	require.Len(t, result.Publications, 3)
	require.Len(t, creator.commands, 3)

	for index, command := range creator.commands {
		require.Equal(t, "ws", command.WorkspaceID)
		require.Equal(t, "short_text", command.ContentProfile)
		require.Equal(t, "post", command.CreationPreset)
		require.Equal(t, []string{"x-1"}, command.SocialAccountIDs)
		require.NotNil(t, command.ScheduledAt)
		require.Equal(t, plan.Slots[index].ScheduledAt, command.ScheduledAt.UTC())
		require.Equal(t, plan.Slots[index].Topic, command.Title)
		require.Equal(t, plan.Slots[index].Text, command.SourceText)
		require.Equal(t, plan.Slots[index].Angle, command.Goal)
	}

	require.Equal(t, "publication-1", result.Publications[0].PublicationID)
	require.Equal(t, 1, result.Publications[0].SlotIndex)
	require.Equal(t, "Analytics launch", result.Publications[0].Topic)
	require.NotEmpty(t, result.Publications[0].ScheduledAt)
}

// TestLandReportsHowManyDraftsSurvivedAFailure keeps the partial case honest:
// drafts already created are kept, and the error says how far it got instead of
// implying nothing happened.
func TestLandReportsHowManyDraftsSurvivedAFailure(t *testing.T) {
	generator := &planGenerator{response: planJSON("One", "Two", "Three")}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	request := testRequest()
	plan, err := service.Plan(context.Background(), request)
	require.NoError(t, err)

	creator := &recordingCreator{failAt: 2, err: errors.New("destination rejected")}
	result, err := service.Land(context.Background(), "user", request, plan, creator)
	require.Error(t, err)
	require.ErrorContains(t, err, "draft 2 of 3")

	// The first draft landed and is reported; the third was never attempted.
	require.Len(t, result.Publications, 1)
	require.Equal(t, "publication-1", result.Publications[0].PublicationID)
	require.Len(t, creator.commands, 2)
}

func TestLandRejectsAnEmptyPlan(t *testing.T) {
	generator := &planGenerator{}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	creator := &recordingCreator{}
	_, err = service.Land(context.Background(), "user", testRequest(), Plan{}, creator)
	require.ErrorIs(t, err, ErrNoSlots)
	require.Empty(t, creator.commands)
}

func TestLandRejectsAMissingCreator(t *testing.T) {
	generator := &planGenerator{}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	_, err = service.Land(context.Background(), "user", testRequest(), Plan{Slots: []Slot{{Index: 1, Topic: "One"}}}, nil)
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestLandValidatesTheRequest(t *testing.T) {
	generator := &planGenerator{}
	service, err := New(generator, "test/model")
	require.NoError(t, err)

	request := testRequest()
	request.WorkspaceID = ""

	creator := &recordingCreator{}
	_, err = service.Land(context.Background(), "user", request, Plan{Slots: []Slot{{Index: 1, Topic: "One"}}}, creator)
	require.ErrorIs(t, err, ErrInvalidInput)
	require.Empty(t, creator.commands)
}
