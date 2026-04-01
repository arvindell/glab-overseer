package actions

import "testing"

func TestParseActionSupportsNotifyFallback(t *testing.T) {
	action, err := ParseAction("notify")
	if err != nil {
		t.Fatalf("expected notify fallback, got error: %v", err)
	}
	if action != ActionLog {
		t.Fatalf("expected notify to map to log, got %s", action)
	}
}

func TestParseActionRejectsUnknown(t *testing.T) {
	if _, err := ParseAction("explode"); err == nil {
		t.Fatal("expected invalid action to fail")
	}
}
