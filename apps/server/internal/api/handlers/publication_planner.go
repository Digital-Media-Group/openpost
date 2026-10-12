package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openpost/backend/internal/api/middleware"
	"github.com/openpost/backend/internal/services/publicationplanner"
	"github.com/openpost/backend/internal/services/ratelimit"
	"github.com/openpost/backend/internal/services/workspaceaccess"
	"github.com/uptrace/bun"
)

const (
	publicationPlansPath             = "/publication-plans"
	publicationPlansTag              = "Publication Plans"
	publicationPlanRequestsPerMinute = 6
)

// PublicationWeekPlanner is the narrow capability this handler needs. It is an
// interface so the HTTP layer can be exercised without a model provider.
type PublicationWeekPlanner interface {
	Plan(context.Context, publicationplanner.Request) (publicationplanner.Plan, error)
	Land(context.Context, string, publicationplanner.Request, publicationplanner.Plan, publicationplanner.PublicationCreator) (publicationplanner.LandResult, error)
}

// PublicationPlannerHandler plans a period of drafts and lands the plan.
//
// Planning and landing are separate operations on purpose. Planning writes
// nothing, so an author can read a week, dislike it, and ask again without
// leaving anything behind. Landing is the only step that creates drafts, and it
// creates them as drafts, which cannot be delivered without a separate
// scheduling command.
type PublicationPlannerHandler struct {
	db           *bun.DB
	auth         middleware.Authenticator
	planner      PublicationWeekPlanner
	publications publicationplanner.PublicationCreator
	limiter      *ratelimit.Limiter
}

func NewPublicationPlannerHandler(
	db *bun.DB,
	auth middleware.Authenticator,
	planner PublicationWeekPlanner,
) *PublicationPlannerHandler {
	return &PublicationPlannerHandler{db: db, auth: auth, planner: planner, limiter: ratelimit.New()}
}

// SetPublicationApplication gives the handler the canonical Publication write
// path, so a plan lands through the same seam the composer uses.
func (h *PublicationPlannerHandler) SetPublicationApplication(publications publicationplanner.PublicationCreator) {
	h.publications = publications
}

// publicationPlanBody is the shared request shape for planning and landing.
type publicationPlanBody struct {
	WorkspaceID string   `json:"workspace_id" doc:"Workspace ID"`
	Brief       string   `json:"brief" maxLength:"2000" doc:"What the period is about"`
	StartDate   string   `json:"start_date" doc:"First day of the period, as YYYY-MM-DD"`
	Days        int      `json:"days,omitempty" minimum:"1" maximum:"31" doc:"Length of the period in days; defaults to 7"`
	Slots       int      `json:"slots" minimum:"1" maximum:"14" doc:"How many posts the period should contain"`
	Hour        int      `json:"hour" minimum:"0" maximum:"23" doc:"Local hour of day each post is planned for"`
	TimeZone    string   `json:"time_zone,omitempty" doc:"IANA time zone the hour is local to; defaults to UTC"`
	AccountIDs  []string `json:"social_account_ids" maxItems:"20" doc:"Destinations every planned post targets"`
	Locale      string   `json:"locale,omitempty" maxLength:"35" doc:"Language tag the drafts should be written in"`
}

type PublicationPlanSlot struct {
	Index       int    `json:"index" doc:"Position in the plan, starting at 1"`
	ScheduledAt string `json:"scheduled_at" doc:"Planned local time for this post"`
	Topic       string `json:"topic" doc:"Short topic for the post"`
	Angle       string `json:"angle" doc:"Why this post is worth reading"`
	Text        string `json:"text" doc:"Draft text, publishable as written"`
}

type PublicationPlanResponse struct {
	Slots []PublicationPlanSlot `json:"slots"`
	Model string                `json:"model"`
}

type PlanPublicationWeekInput struct {
	Body publicationPlanBody
}

type PlanPublicationWeekOutput struct {
	Body PublicationPlanResponse
}

type LandPublicationPlanInput struct {
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"200" doc:"Replay key scoped to the caller and Workspace"`
	Body           struct {
		WorkspaceID string                `json:"workspace_id" doc:"Workspace ID"`
		Brief       string                `json:"brief" maxLength:"2000" doc:"What the period is about"`
		StartDate   string                `json:"start_date" doc:"First day of the period, as YYYY-MM-DD"`
		Days        int                   `json:"days,omitempty" minimum:"1" maximum:"31" doc:"Length of the period in days; defaults to 7"`
		Hour        int                   `json:"hour" minimum:"0" maximum:"23" doc:"Local hour of day each post is planned for"`
		TimeZone    string                `json:"time_zone,omitempty" doc:"IANA time zone the hour is local to; defaults to UTC"`
		AccountIDs  []string              `json:"social_account_ids" maxItems:"20" doc:"Destinations every planned post targets"`
		Locale      string                `json:"locale,omitempty" maxLength:"35" doc:"Language tag the drafts should be written in"`
		Plan        []PublicationPlanSlot `json:"plan" minItems:"1" maxItems:"14" doc:"The plan to land, exactly as it was planned"`
	}
}

type LandedPublication struct {
	SlotIndex     int    `json:"slot_index"`
	PublicationID string `json:"publication_id"`
	ScheduledAt   string `json:"scheduled_at"`
	Topic         string `json:"topic"`
}

type LandPublicationPlanOutput struct {
	Body struct {
		Publications []LandedPublication `json:"publications"`
		// Problem is set when the plan landed only partly. A partial landing is a
		// 200 with this set rather than an error response, because an error
		// response has no body and would hide the drafts that were created.
		Problem string `json:"problem,omitempty" doc:"Set when only some drafts were created"`
	}
}

func (h *PublicationPlannerHandler) RegisterRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "plan-publication-week",
		Method:      http.MethodPost,
		Path:        publicationPlansPath,
		Summary:     "Plan a period of drafts without creating anything",
		Tags:        []string{publicationPlansTag},
		Middlewares: huma.Middlewares{middleware.AuthMiddleware(api, h.auth)},
		Errors:      []int{400, 403, 429, 503},
	}, h.plan)

	huma.Register(api, huma.Operation{
		OperationID: "land-publication-plan",
		Method:      http.MethodPost,
		Path:        publicationPlansPath + "/land",
		Summary:     "Create one draft per planned post",
		Tags:        []string{publicationPlansTag},
		Middlewares: huma.Middlewares{middleware.AuthMiddleware(api, h.auth)},
		Errors:      []int{400, 403, 429, 503},
	}, h.land)
}

func (h *PublicationPlannerHandler) plan(ctx context.Context, input *PlanPublicationWeekInput) (*PlanPublicationWeekOutput, error) {
	request, err := h.prepare(ctx, input.Body)
	if err != nil {
		return nil, err
	}
	plan, err := h.planner.Plan(ctx, request)
	if err != nil {
		return nil, publicationPlanError(err)
	}
	return &PlanPublicationWeekOutput{Body: publicationPlanResponse(plan)}, nil
}

func (h *PublicationPlannerHandler) land(ctx context.Context, input *LandPublicationPlanInput) (*LandPublicationPlanOutput, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, huma.Error400BadRequest("Idempotency-Key is required")
	}
	// The plan being landed is the source of truth for how many posts there are.
	// Deriving the count from it removes a whole class of client/server
	// disagreement, where a stale count would validate a plan of a different size.
	request, err := h.prepare(ctx, publicationPlanBody{
		WorkspaceID: input.Body.WorkspaceID,
		Brief:       input.Body.Brief,
		StartDate:   input.Body.StartDate,
		Days:        input.Body.Days,
		Slots:       len(input.Body.Plan),
		Hour:        input.Body.Hour,
		TimeZone:    input.Body.TimeZone,
		AccountIDs:  input.Body.AccountIDs,
		Locale:      input.Body.Locale,
	})
	if err != nil {
		return nil, err
	}
	if h.publications == nil {
		return nil, huma.Error503ServiceUnavailable("Publication creation is unavailable")
	}

	plan, err := publicationPlanFromRequest(input.Body.Plan)
	if err != nil {
		return nil, err
	}

	result, err := h.planner.Land(ctx, middleware.GetUserID(ctx), request, plan, h.publications)
	if err != nil {
		// A partial landing still created drafts. An error response carries no
		// body, so reporting the failure that way would hide the drafts that now
		// exist and invite the author to land the plan again. Answer 200 with the
		// drafts that were created and name the problem instead.
		if len(result.Publications) > 0 {
			out := &LandPublicationPlanOutput{}
			out.Body.Publications = landedPublications(result)
			out.Body.Problem = "Only some drafts were created. The rest failed; the drafts already created were kept."
			return out, nil
		}
		return nil, publicationPlanError(err)
	}

	out := &LandPublicationPlanOutput{}
	out.Body.Publications = landedPublications(result)
	return out, nil
}

// prepare validates the shared body and authorizes the caller. Creating drafts
// is authoring, so both operations require the edit level; planning is gated too
// because it spends a model call on the workspace's behalf.
func (h *PublicationPlannerHandler) prepare(ctx context.Context, body publicationPlanBody) (publicationplanner.Request, error) {
	if h.planner == nil {
		return publicationplanner.Request{}, huma.Error503ServiceUnavailable("Publication planning is unavailable")
	}
	userID := strings.TrimSpace(middleware.GetUserID(ctx))
	if userID == "" {
		return publicationplanner.Request{}, huma.Error401Unauthorized("authentication required")
	}
	workspaceID := strings.TrimSpace(body.WorkspaceID)
	if workspaceID == "" || len(workspaceID) > 128 || containsControl(workspaceID, false) {
		return publicationplanner.Request{}, huma.Error400BadRequest("workspace_id is required")
	}
	if h.limiter == nil || !h.limiter.Allow("publication-plan:"+userID, publicationPlanRequestsPerMinute, time.Minute) {
		return publicationplanner.Request{}, huma.Error429TooManyRequests("Publication planning limit reached; try again in one minute")
	}

	decision, err := workspaceaccess.NewAuthorizer(h.db).Authorize(
		ctx, workspaceID, workspaceActor(ctx, userID), workspaceaccess.LevelEdit,
	)
	if err != nil {
		return publicationplanner.Request{}, huma.Error503ServiceUnavailable("Workspace access could not be checked")
	}
	if !decision.Allowed {
		return publicationplanner.Request{}, huma.Error403Forbidden("workspace editor role required")
	}

	startDate, err := time.Parse("2006-01-02", strings.TrimSpace(body.StartDate))
	if err != nil {
		return publicationplanner.Request{}, huma.Error400BadRequest("start_date must be YYYY-MM-DD")
	}

	location := time.UTC
	if name := strings.TrimSpace(body.TimeZone); name != "" {
		loaded, err := time.LoadLocation(name)
		if err != nil {
			return publicationplanner.Request{}, huma.Error400BadRequest("time_zone must be an IANA time zone name")
		}
		location = loaded
	}

	return publicationplanner.Request{
		WorkspaceID: workspaceID,
		Brief:       body.Brief,
		StartDate:   startDate,
		Days:        body.Days,
		Slots:       body.Slots,
		Hour:        body.Hour,
		Location:    location,
		AccountIDs:  body.AccountIDs,
		Locale:      body.Locale,
	}, nil
}

// publicationPlanFromRequest rebuilds the plan the author was shown. The model
// output travels back through the client rather than being stored, so landing a
// plan costs no second model call and cannot differ from what was reviewed.
func publicationPlanFromRequest(slots []PublicationPlanSlot) (publicationplanner.Plan, error) {
	if len(slots) == 0 {
		return publicationplanner.Plan{}, huma.Error400BadRequest("slots are required")
	}
	plan := publicationplanner.Plan{Slots: make([]publicationplanner.Slot, 0, len(slots))}
	for _, slot := range slots {
		scheduledAt, err := time.Parse(time.RFC3339, strings.TrimSpace(slot.ScheduledAt))
		if err != nil {
			return publicationplanner.Plan{}, huma.Error400BadRequest("each slot needs an RFC 3339 scheduled_at")
		}
		plan.Slots = append(plan.Slots, publicationplanner.Slot{
			Index:       slot.Index,
			ScheduledAt: scheduledAt,
			Topic:       strings.TrimSpace(slot.Topic),
			Angle:       strings.TrimSpace(slot.Angle),
			Text:        strings.TrimSpace(slot.Text),
		})
	}
	return plan, nil
}

func publicationPlanResponse(plan publicationplanner.Plan) PublicationPlanResponse {
	slots := make([]PublicationPlanSlot, 0, len(plan.Slots))
	for _, slot := range plan.Slots {
		slots = append(slots, PublicationPlanSlot{
			Index:       slot.Index,
			ScheduledAt: slot.ScheduledAt.Format(time.RFC3339),
			Topic:       slot.Topic,
			Angle:       slot.Angle,
			Text:        slot.Text,
		})
	}
	return PublicationPlanResponse{Slots: slots, Model: plan.Model}
}

func landedPublications(result publicationplanner.LandResult) []LandedPublication {
	landed := make([]LandedPublication, 0, len(result.Publications))
	for _, item := range result.Publications {
		landed = append(landed, LandedPublication{
			SlotIndex:     item.SlotIndex,
			PublicationID: item.PublicationID,
			ScheduledAt:   item.ScheduledAt,
			Topic:         item.Topic,
		})
	}
	return landed
}

func publicationPlanError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, publicationplanner.ErrInvalidInput):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, publicationplanner.ErrPlanRejected):
		return huma.Error502BadGateway("The plan could not be produced; try again")
	default:
		return huma.Error503ServiceUnavailable("Publication planning failed")
	}
}
