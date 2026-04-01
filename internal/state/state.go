package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	path string
	mu   sync.Mutex
	data fileState
}

type fileState struct {
	LastSeen map[string]int64 `json:"last_seen"`
}

func NewStore(path string) (*Store, error) {
	store := &Store{
		path: path,
		data: fileState{LastSeen: map[string]int64{}},
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}

	if err := json.Unmarshal(contents, &store.data); err != nil {
		return nil, fmt.Errorf("parse state file: %w", err)
	}

	if store.data.LastSeen == nil {
		store.data.LastSeen = map[string]int64{}
	}

	return store, nil
}

func (s *Store) Get(key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.LastSeen[key]
}

func (s *Store) Set(key string, value int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.LastSeen[key] = value

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	encoded, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	if err := os.WriteFile(s.path, encoded, 0o600); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}

	return nil
}
