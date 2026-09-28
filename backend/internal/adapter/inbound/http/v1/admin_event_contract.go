package v1

import "context"

const (
	AdminEventTopicPlayers     = "players"
	AdminEventTopicTasks       = "tasks"
	AdminEventTopicTournaments = "tournaments"
)

type AdminEventSubscriber interface {
	SubscribeAdminChanges(ctx context.Context) (<-chan string, func(), error)
}
