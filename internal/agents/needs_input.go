package agents

import "strings"

const needsInputMarker = "TURNYARD_NEEDS_INPUT:"

// NeedsInputQuestion only accepts an explicit marker on the final line of the
// final assistant message. Earlier mentions and negative explanations are not
// requests for a human decision.
func NeedsInputQuestion(texts []string) string {
	if len(texts) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(texts[len(texts)-1]), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(line, needsInputMarker) {
		return ""
	}
	question := strings.TrimSpace(strings.TrimPrefix(line, needsInputMarker))
	if question == "" {
		return ""
	}
	lower := strings.ToLower(question)
	for _, negative := range []string{"无", "不需要", "无需", "没有", "none", "no", "not needed", "n/a"} {
		if lower == negative || strings.HasPrefix(lower, negative+" — ") ||
			strings.HasPrefix(lower, negative+" - ") || strings.HasPrefix(lower, negative+"。") ||
			strings.HasPrefix(lower, negative+". ") {
			return ""
		}
	}
	return question
}
