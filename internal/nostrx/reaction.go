package nostrx

import (
	"errors"
	"strings"
)

// ReactionLastETagID returns the last "e" tag value on a kind-7 event (NIP-25).
func ReactionLastETagID(event Event) string {
	var last string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "e" {
			continue
		}
		if tag[1] != "" {
			last = tag[1]
		}
	}
	return last
}

// ReactionPolarity returns +1 for upvote (+ or empty content), -1 for downvote,
// or 0 when the content should not be aggregated as up/down (emoji, etc.).
func ReactionPolarity(content string) int {
	switch strings.TrimSpace(content) {
	case "+", "":
		return 1
	case "-":
		return -1
	default:
		return 0
	}
}

// ValidateReactionHTTPAPIShape enforces minimal NIP-25 shape for API publish.
func ValidateReactionHTTPAPIShape(event Event) error {
	if event.Kind != KindReaction {
		return errors.New("not a reaction event")
	}
	switch ReactionPolarity(event.Content) {
	case 1, -1:
	default:
		return errors.New("reaction content must be +, -, or empty (upvote)")
	}
	if ReactionLastETagID(event) == "" {
		return errors.New("reaction requires an e tag")
	}
	return nil
}
