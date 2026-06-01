package tools

import (
	"fmt"
	"strings"
)

const approvalPreviewLimit = 1200

func contentChangePreview(before, after string) string {
	before = strings.ReplaceAll(before, "\r\n", "\n")
	after = strings.ReplaceAll(after, "\r\n", "\n")
	if before == after {
		return "preview: no content changes"
	}
	beforeRunes := []rune(before)
	afterRunes := []rune(after)
	beforeStart, beforeEnd, afterStart, afterEnd := changedRegions(beforeRunes, afterRunes)
	return fmt.Sprintf(
		"--- before ---\n%s\n--- after ---\n%s",
		previewChangedRunes(beforeRunes, beforeStart, beforeEnd),
		previewChangedRunes(afterRunes, afterStart, afterEnd),
	)
}

func changedRegions(before, after []rune) (beforeStart, beforeEnd, afterStart, afterEnd int) {
	limit := min(len(before), len(after))
	for beforeStart < limit && before[beforeStart] == after[beforeStart] {
		beforeStart++
	}
	beforeEnd = len(before)
	afterStart = beforeStart
	afterEnd = len(after)
	for beforeEnd > beforeStart && afterEnd > afterStart && before[beforeEnd-1] == after[afterEnd-1] {
		beforeEnd--
		afterEnd--
	}
	return beforeStart, beforeEnd, afterStart, afterEnd
}

func previewChangedRunes(runes []rune, start, end int) string {
	if len(runes) <= approvalPreviewLimit {
		return strings.TrimRight(string(runes), "\n")
	}
	if start < 0 {
		start = 0
	}
	if start > len(runes) {
		start = len(runes)
	}
	if end < start {
		end = start
	}
	if end > len(runes) {
		end = len(runes)
	}

	const contextRunes = 200
	windowStart := max(0, start-contextRunes)
	windowEnd := min(len(runes), end+contextRunes)
	if windowEnd-windowStart > approvalPreviewLimit {
		windowEnd = min(len(runes), windowStart+approvalPreviewLimit)
	}

	preview := strings.TrimRight(string(runes[windowStart:windowEnd]), "\n")
	if windowStart > 0 {
		preview = "... truncated before change ...\n" + preview
	}
	if windowEnd < len(runes) {
		preview += "\n... truncated after change ..."
	}
	return preview
}
