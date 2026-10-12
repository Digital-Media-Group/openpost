package publicationplanner

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestScheduleSpreadsSlotsAcrossThePeriod pins the arithmetic that decides the
// calendar. The model is never asked for a date, so this function is the only
// source of truth for when a planned post lands; these cases are the contract.
func TestScheduleSpreadsSlotsAcrossThePeriod(t *testing.T) {
	start := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC) // a Monday

	cases := []struct {
		name    string
		days    int
		count   int
		hour    int
		wantDay []int
	}{
		{name: "three over a week", days: 7, count: 3, hour: 9, wantDay: []int{2, 4, 6}},
		{name: "five over a week", days: 7, count: 5, hour: 9, wantDay: []int{2, 3, 4, 6, 7}},
		{name: "one over a week", days: 7, count: 1, hour: 9, wantDay: []int{2}},
		{name: "a full week", days: 7, count: 7, hour: 9, wantDay: []int{2, 3, 4, 5, 6, 7, 8}},
		{name: "two over two days", days: 2, count: 2, hour: 14, wantDay: []int{2, 3}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slots, err := scheduleSlots(start, tc.days, tc.count, tc.hour, time.UTC)
			require.NoError(t, err)
			require.Len(t, slots, tc.count)

			for index, instant := range slots {
				require.Equal(t, tc.wantDay[index], instant.Day(), "slot %d", index)
				require.Equal(t, tc.hour, instant.Hour(), "slot %d", index)
				require.Equal(t, time.UTC, instant.Location())
			}

			// The days are strictly increasing, so no two posts share a day.
			for index := 1; index < len(slots); index++ {
				require.True(t, slots[index].After(slots[index-1]), "slot %d must follow slot %d", index, index-1)
			}
		})
	}
}

// TestScheduleIsDeterministic proves the same request always yields the same
// instants, which is what lets the caller render a plan and land it later
// without the calendar shifting in between.
func TestScheduleIsDeterministic(t *testing.T) {
	start := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	first, err := scheduleSlots(start, 14, 6, 8, time.UTC)
	require.NoError(t, err)
	second, err := scheduleSlots(start, 14, 6, 8, time.UTC)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

// TestScheduleUsesTheAuthorLocalHour checks the timezone is honoured: a post
// planned for 09:00 in Madrid is 07:00 or 08:00 UTC depending on the season, and
// never 09:00 UTC.
func TestScheduleUsesTheAuthorLocalHour(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)

	// January: Madrid is UTC+1, so 09:00 local is 08:00 UTC.
	winter, err := scheduleSlots(time.Date(2026, time.January, 5, 0, 0, 0, 0, madrid), 3, 1, 9, madrid)
	require.NoError(t, err)
	require.Equal(t, 8, winter[0].UTC().Hour())

	// July: Madrid is UTC+2, so 09:00 local is 07:00 UTC.
	summer, err := scheduleSlots(time.Date(2026, time.July, 6, 0, 0, 0, 0, madrid), 3, 1, 9, madrid)
	require.NoError(t, err)
	require.Equal(t, 7, summer[0].UTC().Hour())

	// Both are 09:00 on the author's clock, which is the point.
	require.Equal(t, 9, winter[0].In(madrid).Hour())
	require.Equal(t, 9, summer[0].In(madrid).Hour())
}

// TestScheduleKeepsTheLocalHourAcrossADaylightSavingTransition is the case a
// naive "add 24 hours" loop gets wrong: it would drift by an hour the day the
// clocks change. Building each instant from calendar parts keeps 09:00 at 09:00.
func TestScheduleKeepsTheLocalHourAcrossADaylightSavingTransition(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)

	// Spain moves its clocks forward on the last Sunday of March. This window
	// covers 2026-03-29.
	start := time.Date(2026, time.March, 25, 0, 0, 0, 0, madrid)
	slots, err := scheduleSlots(start, 7, 7, 9, madrid)
	require.NoError(t, err)
	require.Len(t, slots, 7)

	for index, instant := range slots {
		require.Equal(t, 9, instant.In(madrid).Hour(), "slot %d must stay at 09:00 local", index)
	}
}

// TestScheduleRejectsImpossibleRequests keeps the guards honest, since the
// request is untrusted input from an API caller.
func TestScheduleRejectsImpossibleRequests(t *testing.T) {
	start := time.Date(2026, time.March, 2, 0, 0, 0, 0, time.UTC)

	_, err := scheduleSlots(start, 0, 1, 9, time.UTC)
	require.ErrorIs(t, err, ErrInvalidInput)

	_, err = scheduleSlots(start, 7, 0, 9, time.UTC)
	require.ErrorIs(t, err, ErrInvalidInput)

	// More posts than days would put two on one day.
	_, err = scheduleSlots(start, 3, 5, 9, time.UTC)
	require.ErrorIs(t, err, ErrInvalidInput)

	_, err = scheduleSlots(start, 7, 3, 24, time.UTC)
	require.ErrorIs(t, err, ErrInvalidInput)

	_, err = scheduleSlots(start, 7, 3, -1, time.UTC)
	require.ErrorIs(t, err, ErrInvalidInput)
}
