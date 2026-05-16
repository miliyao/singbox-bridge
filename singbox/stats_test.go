package singbox

import "testing"

func TestParseUserID(t *testing.T) {
	got, err := parseUserID("user-123")
	if err != nil {
		t.Fatalf("parseUserID returned error: %v", err)
	}
	if got != 123 {
		t.Fatalf("parseUserID = %d, want 123", got)
	}
}

func TestParseUserIDRejectsInvalidPrefix(t *testing.T) {
	if _, err := parseUserID("client-123"); err == nil {
		t.Fatal("expected invalid prefix error")
	}
}

func TestParseUserIDRejectsInvalidNumber(t *testing.T) {
	if _, err := parseUserID("user-abc"); err == nil {
		t.Fatal("expected invalid number error")
	}
}
