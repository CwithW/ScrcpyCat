package controlplane

import (
	"encoding/json"
	"testing"
)

func TestSnapshotRetainsPasswordHashes(t *testing.T) {
	inner, err := NewMemoryStore("admin", "password")
	if err != nil {
		t.Fatalf("create memory store: %v", err)
	}
	store := &PostgresStore{inner: inner}
	raw, err := store.snapshotJSON()
	if err != nil {
		t.Fatalf("snapshot state: %v", err)
	}
	var state persistedState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	adminID := state.UsersByName["admin"]
	if len(state.Users[adminID].PasswordHash) == 0 {
		t.Fatal("snapshot omitted the administrator password hash")
	}
}
