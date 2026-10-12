package publicationplanner

import (
	"errors"
	"fmt"
	"time"
)

// scheduleSlots places count posts across days days, starting at start, each at
// hour local time.
//
// The placement is arithmetic, not a choice. Slot i lands on day
// floor(i*days/count), so a week of three posts lands on days 0, 2, 4 and a week
// of five lands on 0, 1, 2, 4, 5. The same request therefore always yields the
// same days, and no model output can move a post.
//
// A start whose local clock is skipped or repeated by a daylight-saving
// transition is resolved by Go's time normalisation; the returned instants are
// always real local times, never an invented one.
func scheduleSlots(start time.Time, days, count, hour int, location *time.Location) ([]time.Time, error) {
	if days < 1 {
		return nil, fmt.Errorf("%w: days must be positive", ErrInvalidInput)
	}
	if count < 1 {
		return nil, fmt.Errorf("%w: slots must be positive", ErrInvalidInput)
	}
	if count > days {
		return nil, fmt.Errorf("%w: slots cannot exceed days", ErrInvalidInput)
	}
	if hour < 0 || hour > 23 {
		return nil, fmt.Errorf("%w: hour must be between 0 and 23", ErrInvalidInput)
	}
	if location == nil {
		location = time.UTC
	}

	// Anchor on the start date in the author's zone, at midnight, then set the
	// hour. Building each instant from calendar parts keeps the local hour exact
	// across a daylight-saving transition instead of drifting by an hour.
	local := start.In(location)
	year, month, day := local.Date()

	slots := make([]time.Time, 0, count)
	for index := range count {
		offset := index * days / count
		instant := time.Date(year, month, day+offset, hour, 0, 0, 0, location)
		slots = append(slots, instant.UTC())
	}
	if len(slots) == 0 {
		return nil, ErrNoSlots
	}
	return slots, nil
}

// errSlotCountMismatch reports model output whose item count does not match the
// schedule that was sent to it.
var errSlotCountMismatch = errors.New("publication plan item count does not match the schedule")
