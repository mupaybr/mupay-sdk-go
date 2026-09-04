package mupag

import "testing"

func TestValidResourceIDRejectsExactDotSegments(t *testing.T) {
	for _, value := range []string{".", ".."} {
		if validResourceID(value) {
			t.Fatalf("validResourceID(%q) = true, want false", value)
		}
	}
}

func TestValidResourceIDAcceptsSafeCharactersAndNonSegmentDots(t *testing.T) {
	for _, value := range []string{"charge.1", "AZaz09-_.~"} {
		if !validResourceID(value) {
			t.Fatalf("validResourceID(%q) = false, want true", value)
		}
	}
}
