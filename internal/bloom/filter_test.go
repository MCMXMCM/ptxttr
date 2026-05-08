package bloom

import "testing"

func TestFilterContainsInsertedValues(t *testing.T) {
	filter := New(16)
	filter.Add("alice")
	filter.Add("bob")
	if !filter.Test("alice") {
		t.Fatal("expected alice to be present")
	}
	if !filter.Test("bob") {
		t.Fatal("expected bob to be present")
	}
}

func TestFilterRejectsObviouslyMissingValue(t *testing.T) {
	filter := New(16)
	filter.Add("alice")
	filter.Add("bob")
	if filter.Test("carol") && filter.Test("dave") && filter.Test("erin") {
		t.Fatal("unexpected repeated positives for missing values")
	}
}
