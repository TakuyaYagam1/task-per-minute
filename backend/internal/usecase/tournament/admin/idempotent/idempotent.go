package idempotent

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	executionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	observedusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observed"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	snapshotusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

// Service is the observed admin contract wrapped by the idempotency decorator.
type Service = observedusecase.Service

// IdempotentService adds distributed receipt admission to operator mutations.
// The wrapped service performs all ordinary use case validation and derives
// replayed results from its durable PostgreSQL command records.
type IdempotentService struct {
	next        Service
	catalog     usecase.TournamentUseCase
	coordinator *idempotency.Coordinator
}

func NewIdempotentService(
	next Service,
	catalog usecase.TournamentUseCase,
	coordinator *idempotency.Coordinator,
) (*IdempotentService, error) {
	if next == nil || catalog == nil || coordinator == nil {
		return nil, domain.ErrInternal
	}
	return &IdempotentService{next: next, catalog: catalog, coordinator: coordinator}, nil
}

func (service *IdempotentService) GetRoster(ctx context.Context, query rosterusecase.RosterQuery) (rosterusecase.RosterView, error) {
	if service == nil || service.next == nil {
		return rosterusecase.RosterView{}, domain.ErrInternal
	}
	return service.next.GetRoster(ctx, query)
}

func (service *IdempotentService) ReplaceRoster(
	ctx context.Context,
	command rosterusecase.ReplaceRosterCommand,
) (rosterusecase.RosterView, error) {
	return executeAdminMutation(ctx, service, rosterusecase.ValidReplaceRosterCommand(command), func() (idempotency.Command, error) {
		return replaceRosterReceipt(command)
	}, func() (rosterusecase.RosterView, error) {
		return service.next.ReplaceRoster(ctx, command)
	})
}

func (service *IdempotentService) RunPreflight(
	ctx context.Context,
	command rosterusecase.PreflightCommand,
) (tournamentpreflight.ReportRevision, error) {
	return executeAdminMutation(ctx, service, rosterusecase.ValidPreflightCommand(command), func() (idempotency.Command, error) {
		return preflightReceipt(command)
	}, func() (tournamentpreflight.ReportRevision, error) {
		return service.next.RunPreflight(ctx, command)
	})
}

func (service *IdempotentService) LockRoster(
	ctx context.Context,
	command rosterusecase.LockRosterCommand,
) (rosterusecase.RosterView, error) {
	return executeAdminMutation(ctx, service, rosterusecase.ValidLockRosterCommand(command), func() (idempotency.Command, error) {
		return lockRosterReceipt(command)
	}, func() (rosterusecase.RosterView, error) {
		return service.next.LockRoster(ctx, command)
	})
}

func (service *IdempotentService) UnlockRoster(
	ctx context.Context,
	command rosterusecase.UnlockRosterCommand,
) (rosterusecase.RosterView, error) {
	return executeAdminMutation(ctx, service, rosterusecase.ValidUnlockRosterCommand(command), func() (idempotency.Command, error) {
		return unlockRosterReceipt(command)
	}, func() (rosterusecase.RosterView, error) {
		return service.next.UnlockRoster(ctx, command)
	})
}

func (service *IdempotentService) ConfigurePairings(
	ctx context.Context,
	command pairingusecase.PairingCommand,
) (executionusecase.SwissRoundView, error) {
	return executeAdminMutation(ctx, service, pairingusecase.ValidPairingCommand(command), func() (idempotency.Command, error) {
		return pairingReceipt(command)
	}, func() (executionusecase.SwissRoundView, error) {
		return service.next.ConfigurePairings(ctx, command)
	})
}

func (service *IdempotentService) ApplyTournamentAction(
	ctx context.Context,
	command lifecycleusecase.TournamentActionCommand,
) (usecase.TournamentView, error) {
	return executeAdminMutation(ctx, service, lifecycleusecase.ValidTournamentActionCommand(command), func() (idempotency.Command, error) {
		return tournamentActionReceipt(command)
	}, func() (usecase.TournamentView, error) {
		return service.next.ApplyTournamentAction(ctx, command)
	})
}

func (service *IdempotentService) DeleteTournament(
	ctx context.Context,
	command lifecycleusecase.TournamentDeletionCommand,
) (lifecycleusecase.TournamentDeletionRecord, error) {
	return executeAdminMutation(ctx, service, lifecycleusecase.ValidTournamentDeletionCommand(command), func() (idempotency.Command, error) {
		return tournamentDeletionReceipt(command)
	}, func() (lifecycleusecase.TournamentDeletionRecord, error) {
		return service.next.DeleteTournament(ctx, command)
	})
}

func (service *IdempotentService) ControlWave(ctx context.Context, command executionusecase.WaveCommand) (executionusecase.WaveView, error) {
	return executeAdminMutation(ctx, service, executionusecase.ValidWaveCommand(command), func() (idempotency.Command, error) {
		return waveReceipt(command)
	}, func() (executionusecase.WaveView, error) {
		return service.next.ControlWave(ctx, command)
	})
}

func (service *IdempotentService) ResolveNoShow(ctx context.Context, command resultusecase.NoShowCommand) error {
	_, err := executeAdminMutation(ctx, service, resultusecase.ValidNoShowCommand(command), func() (idempotency.Command, error) {
		return noShowReceipt(command)
	}, func() (struct{}, error) {
		return struct{}{}, service.next.ResolveNoShow(ctx, command)
	})
	return err
}

func (service *IdempotentService) AssignReserve(ctx context.Context, command replayusecase.ReserveCommand) error {
	_, err := executeAdminMutation(ctx, service, replayusecase.ValidReserveCommand(command), func() (idempotency.Command, error) {
		return reserveReceipt(command)
	}, func() (struct{}, error) {
		return struct{}{}, service.next.AssignReserve(ctx, command)
	})
	return err
}

func (service *IdempotentService) RecordForfeit(ctx context.Context, command resultusecase.ForfeitCommand) error {
	_, err := executeAdminMutation(ctx, service, resultusecase.ValidForfeitCommand(command), func() (idempotency.Command, error) {
		return forfeitReceipt(command)
	}, func() (struct{}, error) {
		return struct{}{}, service.next.RecordForfeit(ctx, command)
	})
	return err
}

func (service *IdempotentService) ReplayGame(ctx context.Context, command replayusecase.ReplayCommand) error {
	_, err := executeAdminMutation(ctx, service, replayusecase.ValidReplayCommand(command), func() (idempotency.Command, error) {
		return replayReceipt(command)
	}, func() (struct{}, error) {
		return struct{}{}, service.next.ReplayGame(ctx, command)
	})
	return err
}

func (service *IdempotentService) CorrectGameResult(
	ctx context.Context,
	command correctionusecase.CorrectionCommand,
) (correctionusecase.CorrectionEvidence, error) {
	return executeAdminMutation(ctx, service, correctionusecase.ValidCorrectionCommandWithSource(command), func() (idempotency.Command, error) {
		return correctionReceipt(command)
	}, func() (correctionusecase.CorrectionEvidence, error) {
		return service.next.CorrectGameResult(ctx, command)
	})
}

func (service *IdempotentService) PrepareGameResultCorrection(
	ctx context.Context,
	command correctionusecase.CorrectionCommand,
) (correctionusecase.CorrectionCommand, error) {
	if service == nil || service.next == nil {
		return correctionusecase.CorrectionCommand{}, domain.ErrInternal
	}
	preparer, ok := service.next.(interface {
		PrepareGameResultCorrection(ctx context.Context, command correctionusecase.CorrectionCommand) (correctionusecase.CorrectionCommand, error)
	})
	if !ok {
		return correctionusecase.CorrectionCommand{}, domain.ErrInternal
	}
	return preparer.PrepareGameResultCorrection(ctx, command)
}

func (service *IdempotentService) ListAudit(ctx context.Context, query incidentusecase.AuditQuery) (audit.AuditPage, error) {
	if service == nil || service.next == nil {
		return audit.AuditPage{}, domain.ErrInternal
	}
	return service.next.ListAudit(ctx, query)
}

func (service *IdempotentService) ExportIncident(ctx context.Context, query incidentusecase.IncidentQuery) (audit.IncidentBundle, error) {
	if service == nil || service.next == nil {
		return audit.IncidentBundle{}, domain.ErrInternal
	}
	return service.next.ExportIncident(ctx, query)
}

func (service *IdempotentService) GetOperatorSnapshot(
	ctx context.Context,
	query snapshotusecase.SnapshotQuery,
) (snapshotusecase.OperatorSnapshotView, error) {
	if service == nil || service.next == nil {
		return snapshotusecase.OperatorSnapshotView{}, domain.ErrInternal
	}
	return service.next.GetOperatorSnapshot(ctx, query)
}

// CreateTournament keeps its existing tournament-create receipt boundary.
func (service *IdempotentService) ListTournaments(
	ctx context.Context,
	command usecase.TournamentListCommand,
) (usecase.TournamentPage, error) {
	if service == nil || service.catalog == nil {
		return usecase.TournamentPage{}, domain.ErrInternal
	}
	return service.catalog.ListTournaments(ctx, command)
}

func (service *IdempotentService) CreateTournament(
	ctx context.Context,
	command usecase.TournamentCreateCommand,
) (usecase.TournamentResult, error) {
	if service == nil || service.catalog == nil {
		return usecase.TournamentResult{}, domain.ErrInternal
	}
	return service.catalog.CreateTournament(ctx, command)
}

func (service *IdempotentService) GetTournamentContent(
	ctx context.Context,
	operator usecase.OperatorIdentity,
) (usecase.TournamentContentView, error) {
	if service == nil || service.catalog == nil {
		return usecase.TournamentContentView{}, domain.ErrInternal
	}
	return service.catalog.GetTournamentContent(ctx, operator)
}

func executeAdminMutation[T any](
	ctx context.Context,
	service *IdempotentService,
	valid bool,
	receipt func() (idempotency.Command, error),
	invoke func() (T, error),
) (T, error) {
	var zero T
	if ctx == nil || !valid {
		return zero, domain.ErrValidation
	}
	if service == nil || service.next == nil || service.coordinator == nil {
		return zero, domain.ErrInternal
	}
	command, err := receipt()
	if err != nil {
		return zero, domain.ErrInternal
	}
	return idempotency.Execute(ctx, service.coordinator, command, func(context.Context) (T, error) {
		return invoke()
	})
}

var _ Service = (*IdempotentService)(nil)
var _ usecase.TournamentUseCase = (*IdempotentService)(nil)
