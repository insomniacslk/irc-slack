package main

import (
	"strings"
)

// WordWrap wraps the given words up to the maximum specified length.
// If a single word is longer than the max length, it is truncated.
func WordWrap(allWords []string, maxLen int) []string {
	var (
		lines  []string
		curLen int
		words  []string
	)
	for _, word := range allWords {
		// curLen + len(words) + len(word) is the length of the current
		// line including spaces
		if curLen+len(words)+len(word) > maxLen {
			// we have our line. That does not include the current word
			lines = append(lines, strings.Join(words, " "))
			// reset the current line, add the current word
			words = []string{word}
			curLen = len(word)
		} else {
			words = append(words, word)
			curLen += len(word)
		}
	}
	if len(words) > 0 {
		// there's one last line to add
		lines = append(lines, strings.Join(words, " "))
	}
	for idx, line := range lines {
		if len(line) > maxLen {
			// truncate
			lines[idx] = line[:maxLen]
		}
	}
	return lines
}
