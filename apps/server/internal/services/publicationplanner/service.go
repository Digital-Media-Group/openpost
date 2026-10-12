package publicationplanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openpost/backend/internal/ai"
)

// Service plans a period of posts. It holds no database and writes nothing: it
// turns a brief into a plan, and landing that plan is a separate, explicit step.
type Service struct {
	generator ai.Generator
	model     string
	timeout   time.Duration
	now       func() time.Time
}

// New builds a planner. A missing generator or model is a construction error,
// not a runtime one, so a misconfigured instance fails at startup.
func New(generator ai.Generator, model string) (*Service, error) {
	if generator == nil {
		return nil, fmt.Errorf("%w: generator is required", ErrInvalidInput)
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("%w: model is required", ErrInvalidInput)
	}
	return &Service{
		generator: generator,
		model:     model,
		timeout:   defaultTimeout,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

// Plan decides the period's posts. It performs exactly one model call and makes
// no writes, so a caller may show the plan, discard it, or land it.
func (s *Service) Plan(ctx context.Context, request Request) (Plan, error) {
	if s == nil || s.generator == nil {
		return Plan{}, fmt.Errorf("%w: generator is unavailable", ErrInvalidInput)
	}
	request, err := request.normalize()
	if err != nil {
		return Plan{}, err
	}

	schedule, err := scheduleSlots(request.StartDate, request.Days, request.Slots, request.Hour, request.Location)
	if err != nil {
		return Plan{}, err
	}

	userPrompt, err := planUserPrompt(request, schedule)
	if err != nil {
		return Plan{}, err
	}

	requestCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	generated, err := s.generator.Generate(requestCtx, ai.GenerateRequest{
		Model:           s.model,
		SystemPrompt:    planSystemPrompt,
		UserPrompt:      userPrompt,
		ResponseSchema:  planResponseSchema(len(schedule)),
		MaxOutputTokens: planOutputTokens(len(schedule)),
		ReasoningEffort: ai.ReasoningEffortMedium,
	})
	if err != nil {
		return Plan{}, fmt.Errorf("generate publication plan: %w", err)
	}

	var response planResponse
	if err := decodeStrictJSON(generated.Text, &response); err != nil {
		return Plan{}, fmt.Errorf("%w: %v", ErrPlanRejected, err)
	}
	if len(response.Posts) != len(schedule) {
		return Plan{}, fmt.Errorf("%w: %v: got %d, want %d", ErrPlanRejected, errSlotCountMismatch, len(response.Posts), len(schedule))
	}

	slots := make([]Slot, 0, len(schedule))
	for index, instant := range schedule {
		post := response.Posts[index]
		topic := strings.TrimSpace(post.Topic)
		angle := strings.TrimSpace(post.Angle)
		text := strings.TrimSpace(post.Text)
		if topic == "" || angle == "" || text == "" {
			return Plan{}, fmt.Errorf("%w: slot %d is incomplete", ErrPlanRejected, index+1)
		}
		slots = append(slots, Slot{
			Index:       index + 1,
			ScheduledAt: instant,
			Topic:       topic,
			Angle:       angle,
			Text:        text,
		})
	}

	model := strings.TrimSpace(generated.Model)
	if model == "" {
		model = s.model
	}
	return Plan{Slots: slots, Model: model}, nil
}

// planOutputTokens budgets the response from the number of posts. A fixed
// budget would truncate a long plan, and a generous one would waste room on a
// short plan; the schema already bounds each field.
func planOutputTokens(count int) int64 {
	const perPost = 1_100
	const floor = 2_000
	budget := count * perPost
	if budget < floor {
		return floor
	}
	return int64(budget)
}

// decodeStrictJSON decodes model output into a machine-owned shape. Unknown
// fields are refused rather than ignored, so a field the schema does not declare
// cannot arrive unnoticed.
func decodeStrictJSON(text string, target any) error {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimSpace(trimmed)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode structured AI output: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("decode structured AI output: trailing data")
	}
	return nil
}
