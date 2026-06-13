package parser

import (
	"unicode"
)

// ChunkText intelligently splits text into chunks up to maxChunkSize.
// It avoids breaking words by scanning backwards for a newline or space.
func ChunkText(text string, maxChunkSize int) []string {
	if maxChunkSize <= 0 {
		maxChunkSize = 6000 // Default to 6,000 characters
	}

	var chunks []string
	runes := []rune(text)

	for len(runes) > 0 {
		if len(runes) <= maxChunkSize {
			chunks = append(chunks, string(runes))
			break
		}

		// Find the best split point within maxChunkSize limit
		splitIdx := maxChunkSize
		found := false

		// Scan backwards to find a whitespace character (preferably a newline)
		for i := maxChunkSize; i > 0; i-- {
			if runes[i] == '\n' {
				splitIdx = i
				found = true
				break
			}
			if !found && unicode.IsSpace(runes[i]) {
				splitIdx = i
				found = true
				// We keep looking backwards to see if there's a better \n split,
				// but we remember this space as a fallback.
			}
		}

		// If no whitespace is found at all, we're forced to do a hard split.
		if !found {
			splitIdx = maxChunkSize
		}

		chunks = append(chunks, string(runes[:splitIdx]))

		// Advance the slice, skipping the matched whitespace character if we found one
		if found && splitIdx < len(runes) {
			runes = runes[splitIdx+1:]
		} else {
			runes = runes[splitIdx:]
		}
	}

	return chunks
}
