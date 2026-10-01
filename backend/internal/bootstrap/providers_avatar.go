package bootstrap

import (
	"github.com/google/wire"

	mediaffmpeg "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/media/ffmpeg"
	avatarrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/avatar"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	avatarusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/avatar"
)

var PlayerAvatarsSet = wire.NewSet(
	provideAvatarVideoProcessor,
	providePlayerAvatarService,
	providePublicPlayerAvatarService,
	providePlayerAvatarCleanupWorker,
	wire.Bind(new(inbound.PlayerAvatarService), new(*avatarusecase.Service)),
	wire.Bind(new(inbound.PublicPlayerAvatarService), new(*avatarusecase.PublicService)),
	wire.Bind(new(avatarusecase.VideoProcessor), new(*mediaffmpeg.Processor)),
)

func provideAvatarVideoProcessor() (*mediaffmpeg.Processor, error) {
	return mediaffmpeg.NewProcessor(mediaffmpeg.Config{})
}

func providePublicPlayerAvatarService(
	repository *avatarrepo.Repository,
	objects avatarusecase.ObjectStorage,
) (*avatarusecase.PublicService, error) {
	return avatarusecase.NewPublicService(repository, objects)
}

func providePlayerAvatarService(
	repository *avatarrepo.Repository,
	objects avatarusecase.ObjectStorage,
	clock avatarusecase.Clock,
	video avatarusecase.VideoProcessor,
) (*avatarusecase.Service, error) {
	return avatarusecase.NewService(repository, objects, clock, video)
}

func providePlayerAvatarCleanupWorker(
	repository *avatarrepo.Repository,
	objects avatarusecase.ObjectStorage,
	clock avatarusecase.Clock,
) *avatarusecase.CleanupWorker {
	return avatarusecase.NewCleanupWorker(repository, objects, clock, avatarusecase.CleanupConfig{})
}
