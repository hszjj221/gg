package tools

import (
	"fmt"
	"strings"
)

const approvalPreviewLimit = 1200

func contentChangePreview(before, after string) string {
	if before == after {
		return "preview: no content changes"
	}
	return fmt.Sprintf("--- before ---\n%s\n--- after ---\n%s", previewText(before), previewText(after))
}

func previewText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if len(text) <= approvalPreviewLimit {
		return strings.TrimRight(text, "\n")
	}
	return strings.TrimRight(text[:approvalPreviewLimit], "\n") + "\n... truncated ..."
}
