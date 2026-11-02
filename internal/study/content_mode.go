package study

import "strings"

const (
	ContentModeDefault  = "default"
	ContentModeNews     = "news"
	ContentModeLearning = "learning"
)

// Empty is the canonical stored default. The omitempty wire representation
// preserves legacy content hashes: adding a default mode does not republish or
// advance every existing item's revision when a collector replays its payload.
func normalizeContentMode(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "", ContentModeDefault:
		return "", nil
	case ContentModeNews:
		return ContentModeNews, nil
	case ContentModeLearning:
		return ContentModeLearning, nil
	default:
		return "", ErrInvalid
	}
}
func effectiveContentMode(value string) string {
	if value == "" {
		return ContentModeDefault
	}
	return value
}
func newsContent(content Content) bool {
	mode := effectiveContentMode(content.ContentMode)
	return mode == ContentModeDefault || mode == ContentModeNews
}
