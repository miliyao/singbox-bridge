package core

import (
	"testing"

	"phantom-node/panel"
)

func TestHashUsersIgnoresInputOrder(t *testing.T) {
	usersA := []panel.User{
		{ID: 2, UUID: "uuid-b"},
		{ID: 1, UUID: "uuid-a"},
	}
	usersB := []panel.User{
		{ID: 1, UUID: "uuid-a"},
		{ID: 2, UUID: "uuid-b"},
	}

	if hashUsers(usersA) != hashUsers(usersB) {
		t.Fatal("expected user hash to be independent of slice order")
	}
}

func TestHashConfigHandlesNil(t *testing.T) {
	if got := hashConfig(nil); got != "nil" {
		t.Fatalf("hashConfig(nil) = %q, want %q", got, "nil")
	}
}

func TestHashConfigChangesWhenConfigChanges(t *testing.T) {
	configA := &panel.NodeConfig{Protocol: "vless", ServerPort: 443}
	configB := &panel.NodeConfig{Protocol: "vless", ServerPort: 8443}

	if hashConfig(configA) == hashConfig(configB) {
		t.Fatal("expected different configs to produce different hashes")
	}
}
