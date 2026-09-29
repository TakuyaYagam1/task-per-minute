package bootstrap

import (
	"time"

	"github.com/google/wire"

	notificationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/notification"
	notificationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/notification"
)

var PlayerNotificationsSet = wire.NewSet(
	notificationrepo.NewRepository,
	wire.Bind(new(notificationusecase.Repository), new(*notificationrepo.Repository)),
	notificationrepo.NewEventsPostgres,
	wire.Bind(new(notificationusecase.Subscriber), new(*notificationrepo.EventsPostgres)),
	notificationusecase.New,
	providePlayerNotificationCleanupWorker,
	wire.Bind(new(notificationusecase.PlayerNotifications), new(*notificationusecase.Service)),
	wire.Bind(new(notificationusecase.RemovalRecorder), new(*notificationusecase.Service)),
)

func providePlayerNotificationCleanupWorker(
	repository notificationusecase.Repository,
) *notificationusecase.CleanupWorker {
	return notificationusecase.NewCleanupWorker(repository, time.Minute)
}
