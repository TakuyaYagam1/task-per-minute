package golden

import runtimeusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/runtime"

var ErrInvalidGoldenRuntime = runtimeusecase.ErrInvalidGoldenRuntime

type RuntimeRepository = runtimeusecase.RuntimeRepository
type RuntimeApplication = runtimeusecase.RuntimeApplication

func NewRuntimeApplication(repository RuntimeRepository, clock ConnectionClock) *RuntimeApplication {
	return runtimeusecase.NewRuntimeApplication(repository, clock)
}
