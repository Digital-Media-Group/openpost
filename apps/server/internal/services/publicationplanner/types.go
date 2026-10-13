// Package publicationplanner turns one brief into a week of drafted
// Publications.
//
// It answers a different question than the publication builder. The builder
// takes one idea and makes it ready for every destination. The planner takes a
// period and decides what the period should contain: how many posts, which
// days, and what each one is about. Each slot it decides becomes an ordinary
// draft, so the rest of the product — the composer, the builder, approvals —
// works on it unchanged.
//
// The model chooses ideas and words. It never chooses a timestamp: the schedule
// is computed here, deterministically, so the same request always produces the
// same days and a slow or inventive model cannot silently move a post.
package publicationplanner

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultModel mirrors the model the other authoring features default to.
	DefaultModel = "openai/gpt-6-luna"

	// DefaultDays is one week, the period the feature is named for.
	DefaultDays = 7

	// MaxDays bounds one plan to a month so a single request cannot ask for an
	// unbounded calendar.
	MaxDays = 31

	// MaxSlots bounds how many drafts one request may create. It is deliberately
	// low: this creates real drafts a person then has to read.
	MaxSlots = 14

	// MaxAccountIDs bounds how many destinations one plan may target.
	MaxAccountIDs = 20

	MaxBriefCharacters = 2000
	MaxTopicCharacters = 160
	MaxAngleCharacters = 400
	MaxTextCharacters  = 6000

	defaultTimeout = 120 * time.Second
)

var (
	// ErrInvalidInput reports a request the planner refuses before spending a
	// model call.
	ErrInvalidInput = errors.New("invalid publication plan input")

	// ErrPlanRejected reports model output that does not satisfy the schema it
	// was asked for. The model's own text is not retained.
	ErrPlanRejected = errors.New("publication plan was rejected")

	// ErrNoSlots reports a schedule that produced nothing to ask about.
	ErrNoSlots = errors.New("publication plan has no slots")
)

// Request is one planning request.
type Request struct {
	WorkspaceID string
	// Brief is what the period is about. It is untrusted author input.
	Brief string
	// StartDate is the first day the plan may use. Only its date is read.
	StartDate time.Time
	// Days is the length of the period. Zero means DefaultDays.
	Days int
	// Slots is how many posts the period should contain.
	Slots int
	// Hour is the local hour of day each post is planned for.
	Hour int
	// Location is the author's timezone, used to place each slot at Hour local
	// time. Nil means UTC.
	Location *time.Location
	// AccountIDs are the destinations every slot targets.
	AccountIDs []string
	// Locale is the language the drafts should be written in.
	Locale string
}

// Slot is one planned post. Its time is computed by this package, never by the
// model.
type Slot struct {
	Index       int
	ScheduledAt time.Time
	Topic       string
	Angle       string
	Text        string
}

// Plan is a period's worth of decided posts.
type Plan struct {
	Slots []Slot
	Model string
}

// normalize validates a request and fills its defaults.
func (request Request) normalize() (Request, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	if request.WorkspaceID == "" {
		return Request{}, fmt.Errorf("%w: workspace is required", ErrInvalidInput)
	}

	if err := request.normalizeBrief(); err != nil {
		return Request{}, err
	}
	if err := request.normalizeSchedule(); err != nil {
		return Request{}, err
	}

	accounts, err := normalizeAccountIDs(request.AccountIDs)
	if err != nil {
		return Request{}, err
	}
	request.AccountIDs = accounts
	request.Locale = strings.TrimSpace(request.Locale)
	return request, nil
}

func (request *Request) normalizeBrief() error {
	request.Brief = strings.TrimSpace(request.Brief)
	if request.Brief == "" {
		return fmt.Errorf("%w: brief is required", ErrInvalidInput)
	}
	if utf8.RuneCountInString(request.Brief) > MaxBriefCharacters {
		return fmt.Errorf("%w: brief exceeds %d characters", ErrInvalidInput, MaxBriefCharacters)
	}
	return nil
}

func (request *Request) normalizeSchedule() error {
	if request.Days == 0 {
		request.Days = DefaultDays
	}
	if request.Days < 1 || request.Days > MaxDays {
		return fmt.Errorf("%w: days must be between 1 and %d", ErrInvalidInput, MaxDays)
	}
	if request.Slots < 1 || request.Slots > MaxSlots {
		return fmt.Errorf("%w: slots must be between 1 and %d", ErrInvalidInput, MaxSlots)
	}
	if request.Slots > request.Days {
		return fmt.Errorf("%w: slots cannot exceed days", ErrInvalidInput)
	}
	if request.Hour < 0 || request.Hour > 23 {
		return fmt.Errorf("%w: hour must be between 0 and 23", ErrInvalidInput)
	}
	if request.Location == nil {
		request.Location = time.UTC
	}
	if request.StartDate.IsZero() {
		return fmt.Errorf("%w: start date is required", ErrInvalidInput)
	}
	return nil
}

// normalizeAccountIDs trims the destinations, drops blanks and repeats, and
// enforces the cap. Order is preserved so the plan reads in the order asked for.
func normalizeAccountIDs(ids []string) ([]string, error) {
	accounts := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, accountID := range ids {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" {
			continue
		}
		if _, duplicate := seen[accountID]; duplicate {
			continue
		}
		seen[accountID] = struct{}{}
		accounts = append(accounts, accountID)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("%w: at least one destination is required", ErrInvalidInput)
	}
	if len(accounts) > MaxAccountIDs {
		return nil, fmt.Errorf("%w: at most %d destinations are allowed", ErrInvalidInput, MaxAccountIDs)
	}
	return accounts, nil
}
