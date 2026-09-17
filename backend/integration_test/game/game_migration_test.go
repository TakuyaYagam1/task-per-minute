//go:build integration

package game_test

import (
	"testing"

	gameintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/game"
)

func TestGameMigration(t *testing.T) {
	gameintegration.RunGameMigration(t, sharedPool)
}
