package controller

import (
	"encoding/base64"
	"testing"
)

func TestNewOAuth2State(t *testing.T) {
	stateA, err := newOAuth2State()
	if err != nil {
		t.Fatalf("newOAuth2State returned error: %v", err)
	}
	stateB, err := newOAuth2State()
	if err != nil {
		t.Fatalf("newOAuth2State returned error: %v", err)
	}
	if stateA == stateB {
		t.Fatal("newOAuth2State returned duplicate values")
	}
	raw, err := base64.RawURLEncoding.DecodeString(stateA)
	if err != nil {
		t.Fatalf("state is not raw URL base64: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("decoded state length = %d, want 32", len(raw))
	}
}
