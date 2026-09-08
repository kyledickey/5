package ui

import (
	"regexp"
	"strings"
	"unicode/utf16"
)

// pageLink matches the single-line Markdown links emitted by Quack's record views.
var pageLink = regexp.MustCompile(`\[[^\n]*?\]\([^\n]*?\)`)

// TextPages splits rendered text into lossless UTF-16-bounded pages. It prefers
// whole lines, then spaces, and only splits an uninterrupted line when necessary.
// Callers reserve room for their heading and controls before choosing the limit.
func TextPages(text string, limit int) []string {
	if limit < 2 {
		panic("text page limit must accommodate a Unicode character")
	}
	if text == "" {
		return []string{""}
	}
	var pages []string
	for text != "" {
		units, end := 0, len(text)
		for index, char := range text {
			width := 1
			if char > 0xffff {
				width = 2
			}
			if units+width > limit {
				end = index
				break
			}
			units += width
		}
		if end < len(text) {
			if split := strings.LastIndexByte(text[:end], '\n'); split >= 0 {
				end = split + 1
			} else if split := strings.LastIndexByte(text[:end], ' '); split >= 0 {
				end = split + 1
			}
			// Move a link to the next page rather than cutting its label or URL.
			// A single link longer than the entire budget still has to be split.
			for _, link := range pageLink.FindAllStringIndex(text, -1) {
				if link[0] >= end {
					break
				}
				if link[1] > end {
					if link[0] > 0 {
						end = link[0]
					} else if len(utf16.Encode([]rune(text[:link[1]]))) <= limit {
						end = link[1]
					}
					break
				}
			}
		}
		pages = append(pages, text[:end])
		text = text[end:]
	}
	return pages
}
