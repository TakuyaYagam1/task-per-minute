package configuration

import (
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
)

type OperatorIdentity = operationusecase.OperatorIdentity
type CommandScope = operationusecase.CommandScope

type SwissStandingView = pairingusecase.SwissStandingView
type ManualByeMismatchError = pairingusecase.ManualByeMismatchError

var ErrManualByeMismatch = pairingusecase.ErrManualByeMismatch
