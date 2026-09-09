package tournament

import (
	"encoding/json"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/wirelimits"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validRealtimeString(value string) bool {
	return strings.TrimSpace(value) != "" && wirelimits.StringWithinLimit(value)
}

func validOptionalRealtimeString(value string) bool {
	return value == "" || validRealtimeString(value)
}

func validRealtimeCollections(lengths ...int) bool {
	for _, length := range lengths {
		if !wirelimits.CollectionWithinLimit(length) {
			return false
		}
	}
	return true
}

func validTournamentPresetState(preset, state string) bool {
	return validRealtimeString(preset) && validRealtimeString(state) &&
		domain.TournamentPreset(preset).IsValid() && domain.TournamentState(state).IsValid()
}

func valueWithinWireLimits(value any) bool {
	encoded, err := json.Marshal(value)
	return err == nil && wirelimits.ValidateJSON(encoded) == nil
}
