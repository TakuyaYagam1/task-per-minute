package player

import "github.com/TakuyaYagam1/task-per-minute/internal/domain"

type PlayerWithActiveDuel struct {
	Player     *domain.Player
	ActiveDuel *domain.Duel
}
