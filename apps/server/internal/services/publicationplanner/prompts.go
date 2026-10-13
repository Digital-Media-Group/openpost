package publicationplanner

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const planSystemPrompt = `You plan social media calendars.

You receive a brief and a fixed schedule of posts. For each scheduled post you
return one topic, one angle, and one ready-to-publish text.

Rules:
- Return exactly as many posts as the schedule lists, in the same order.
- Never return a date or a time. The schedule is already fixed.
- Give every post a distinct topic. A week of posts that repeat one idea is a
  failure, even when each wording differs.
- Write the text in the language the brief is written in, unless asked
  otherwise.
- The text must be publishable as written: no placeholders, no brackets, no
  instructions to the author, no notes about what to add later.
- Do not invent facts, figures, dates, prices, names, or quotes that the brief
  does not contain. If a post needs a fact the brief lacks, write the post about
  the idea without the fact.
- The angle says why this post is worth reading, in one sentence. It is internal
  guidance for the author, not part of the published text.`

// planUserPrompt renders the brief and the computed schedule.
//
// The schedule is rendered as ordered calendar dates with a weekday, because a
// weekday is the part of a date that carries planning meaning: a Monday post and
// a Saturday post are different jobs, and a model given only "post 3" cannot
// tell them apart.
func planUserPrompt(request Request, schedule []time.Time) (string, error) {
	if len(schedule) == 0 {
		return "", ErrNoSlots
	}

	type renderedSlot struct {
		Position int    `json:"position"`
		Date     string `json:"date"`
		Weekday  string `json:"weekday"`
	}
	slots := make([]renderedSlot, 0, len(schedule))
	for index, instant := range schedule {
		local := instant.In(request.Location)
		slots = append(slots, renderedSlot{
			Position: index + 1,
			Date:     local.Format("2006-01-02"),
			Weekday:  local.Weekday().String(),
		})
	}
	encodedSlots, err := json.Marshal(slots)
	if err != nil {
		return "", fmt.Errorf("render publication plan schedule: %w", err)
	}

	var builder strings.Builder
	builder.WriteString("Brief:\n")
	builder.WriteString(request.Brief)
	builder.WriteString("\n\nSchedule (")
	fmt.Fprintf(&builder, "%d posts", len(schedule))
	builder.WriteString("):\n")
	builder.Write(encodedSlots)
	builder.WriteString("\n\nReturn one post for each position, in order.")
	if locale := strings.TrimSpace(request.Locale); locale != "" {
		builder.WriteString("\nWrite the text in the language identified by this tag: ")
		builder.WriteString(locale)
		builder.WriteString(".")
	}
	return builder.String(), nil
}
