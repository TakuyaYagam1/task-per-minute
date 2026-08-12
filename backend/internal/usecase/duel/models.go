package duel

import "github.com/TakuyaYagam1/task-per-minute/internal/domain"

type MatchResult struct {
	Duel        *domain.Duel
	Player1Task *domain.Task
	Player2Task *domain.Task
}

type Result struct {
	Correct         bool
	AlreadyFinished bool
	FinishedDuel    *domain.Duel
	Winner          *domain.Player
}

type Detail struct {
	Duel        *domain.Duel
	PlayerTasks []*domain.DuelPlayerTask
}
