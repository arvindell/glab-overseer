package state

import (
	"path/filepath"
	"testing"
)

func TestStorePersistsValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	if err := store.Set("gitlab.com@quentli/quentli", 123); err != nil {
		t.Fatalf("set value: %v", err)
	}

	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}

	if got := reloaded.Get("gitlab.com@quentli/quentli"); got != 123 {
		t.Fatalf("expected persisted value 123, got %d", got)
	}
}
