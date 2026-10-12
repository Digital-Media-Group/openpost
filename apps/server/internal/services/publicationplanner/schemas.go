package publicationplanner

import (
	"fmt"

	"github.com/openpost/backend/internal/ai"
)

// planResponseSchema asks the model for exactly one item per scheduled slot.
//
// The model is given the schedule as an ordered list and must answer with the
// same number of items in the same order. It is never asked for a date: a
// timestamp in model output would be a second source of truth for the calendar,
// and the schema is the cheapest place to refuse one.
func planResponseSchema(count int) *ai.JSONSchema {
	item := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"topic": schemaString(1, MaxTopicCharacters),
			"angle": schemaString(1, MaxAngleCharacters),
			"text":  schemaString(1, MaxTextCharacters),
		},
		"required": []string{"topic", "angle", "text"},
	}
	return &ai.JSONSchema{
		Name:        "openpost_publication_plan",
		Description: fmt.Sprintf("Exactly %d planned posts, in the order of the schedule they were given.", count),
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"posts": map[string]any{
					"type":     "array",
					"minItems": count,
					"maxItems": count,
					"items":    item,
				},
			},
			"required": []string{"posts"},
		},
	}
}

// planResponse mirrors planResponseSchema. Unknown fields are refused by the
// strict decoder, so this struct and the schema must be changed together.
type planResponse struct {
	Posts []struct {
		Topic string `json:"topic"`
		Angle string `json:"angle"`
		Text  string `json:"text"`
	} `json:"posts"`
}

func schemaString(minimum, maximum int) map[string]any {
	return map[string]any{"type": "string", "minLength": minimum, "maxLength": maximum}
}
