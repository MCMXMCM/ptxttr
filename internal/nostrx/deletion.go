package nostrx

import (
	"errors"
	"fmt"
	"strings"
)

const KindEventDeletion = 5

// DeletableNoteKinds are note kinds the web client may delete (NIP-09).
var DeletableNoteKinds = map[int]bool{
	KindTextNote: true,
	KindRepost:   true,
	KindLongForm: true,
}

func CanDeleteEventKind(kind int) bool {
	return DeletableNoteKinds[kind]
}

// DeletionEventIDs returns unique lowercase hex ids from "e" tags on a kind-5 event.
func DeletionEventIDs(event *Event) []string {
	if event == nil || event.Kind != KindEventDeletion {
		return nil
	}
	seen := make(map[string]bool)
	var ids []string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "e" {
			continue
		}
		id := CanonicalHex64(tag[1])
		if len(id) != 64 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func ValidateDeletionHTTPAPIShape(event Event) error {
	if event.Kind != KindEventDeletion {
		return fmt.Errorf("kind %d is not a deletion event", event.Kind)
	}
	ids := DeletionEventIDs(&event)
	if len(ids) == 0 {
		return errors.New("kind 5 requires at least one e tag with a note id")
	}
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "e":
			if len(CanonicalHex64(tag[1])) != 64 {
				return errors.New("kind 5 e tag must contain a 64-character hex event id")
			}
		case "k":
			if strings.TrimSpace(tag[1]) == "" {
				return errors.New("kind 5 k tag must contain a kind number")
			}
		default:
			continue
		}
	}
	return nil
}
