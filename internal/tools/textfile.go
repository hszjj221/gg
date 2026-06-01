package tools

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func readTextPrefix(path string, maxBytes int) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return nil, false, err
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	if isBinaryContent(data) {
		return nil, false, fmt.Errorf("binary file not readable as text")
	}
	return data, truncated, nil
}

func normalizeNewlines(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func splitTextLines(text string) []string {
	lines := strings.Split(normalizeNewlines(text), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isBinaryContent(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func truncationMarker(maxBytes int) string {
	return fmt.Sprintf("\n... truncated after %d bytes ...", maxBytes)
}
