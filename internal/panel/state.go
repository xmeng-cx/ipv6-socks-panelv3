package panel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type StateStore struct {
	path string
	mu   sync.Mutex
}

func NewStateStore(dataDir string) *StateStore {
	return &StateStore{path: filepath.Join(dataDir, "state.json")}
}

func (s *StateStore) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := State{Version: stateVersion, Pending: map[string]PendingOperation{}}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("decode state: %w", err)
	}
	if state.Version != stateVersion {
		return state, fmt.Errorf("unsupported state version %d", state.Version)
	}
	if state.Pending == nil {
		state.Pending = map[string]PendingOperation{}
	}
	return state, nil
}

func (s *StateStore) Save(state State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit state: %w", err)
	}
	return nil
}
