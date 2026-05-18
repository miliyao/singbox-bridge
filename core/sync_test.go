package core

import (
	"testing"

	"phantom-node/panel"
)

func TestHashUsersIgnoresInputOrder(t *testing.T) {
	usersA := []panel.User{
		{ID: 2, UUID: "uuid-b", SpeedLimit: 0},
		{ID: 1, UUID: "uuid-a", SpeedLimit: 0},
	}
	usersB := []panel.User{
		{ID: 1, UUID: "uuid-a", SpeedLimit: 0},
		{ID: 2, UUID: "uuid-b", SpeedLimit: 0},
	}

	if hashUsers(usersA) != hashUsers(usersB) {
		t.Fatal("expected user hash to be independent of slice order")
	}
}

func TestHashUsersChangesWhenSpeedLimitChanges(t *testing.T) {
	usersA := []panel.User{{ID: 1, UUID: "uuid-a", SpeedLimit: 0}}
	usersB := []panel.User{{ID: 1, UUID: "uuid-a", SpeedLimit: 10}}

	if hashUsers(usersA) == hashUsers(usersB) {
		t.Fatal("expected user hash to change when speed limit changes")
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
