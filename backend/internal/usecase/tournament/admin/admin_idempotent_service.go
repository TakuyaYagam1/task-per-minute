package admin

import (
	"context"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

const (
	adminReplaceRosterReceiptNamespace    = "admin-roster-replace"
	adminRunPreflightReceiptNamespace     = "admin-preflight-run"
	adminLockRosterReceiptNamespace       = "admin-roster-lock"
	adminUnlockRosterReceiptNamespace     = "admin-roster-unlock"
	adminPairingReceiptNamespace          = "admin-pairing-configure"
	adminTournamentActionReceiptNamespace = "admin-tournament-action"
	adminWaveReceiptNamespace             = "admin-wave-control"
	adminNoShowReceiptNamespace           = "admin-no-show-resolve"
	adminReserveReceiptNamespace          = "admin-reserve-assign"
	adminForfeitReceiptNamespace          = "admin-forfeit-record"
	adminReplayReceiptNamespace           = "admin-game-replay"
	adminCorrectionReceiptNamespace       = "admin-result-correct"
)

// IdempotentService adds distributed receipt admission to operator mutations.
// The wrapped service performs all ordinary use case validation and derives
// replayed results from its durable PostgreSQL command records.
type AdminIdempotentService struct {
	next        AdminService
	catalog     usecase.TournamentUseCase
	coordinator *idempotency.Coordinator
}

func AdminNewIdempotentService(
	next AdminService,
	catalog usecase.TournamentUseCase,
	coordinator *idempotency.Coordinator,
) (*AdminIdempotentService, error) {
	if next == nil || catalog == nil || coordinator == nil {
		return nil, domain.ErrInternal
	}
	return &AdminIdempotentService{next: next, catalog: catalog, coordinator: coordinator}, nil
}

func (service *AdminIdempotentService) GetRoster(ctx context.Context, query RosterQuery) (RosterView, error) {
	if service == nil || service.next == nil {
		return RosterView{}, domain.ErrInternal
	}
	return service.next.GetRoster(ctx, query)
}

func (service *AdminIdempotentService) ReplaceRoster(
	ctx context.Context,
	command ReplaceRosterCommand,
) (RosterView, error) {
	return executeAdminMutation(ctx, service, validReplaceRosterCommand(command), func() (idempotency.Command, error) {
		return replaceRosterReceipt(command)
	}, func() (RosterView, error) {
		return service.next.ReplaceRoster(ctx, command)
	})
}

func (service *AdminIdempotentService) RunPreflight(
	ctx context.Context,
	command PreflightCommand,
) (tournamentpreflight.ReportRevision, error) {
	return executeAdminMutation(ctx, service, validPreflightCommand(command), func() (idempotency.Command, error) {
		return preflightReceipt(command)
	}, func() (tournamentpreflight.ReportRevision, error) {
		return service.next.RunPreflight(ctx, command)
	})
}

func (service *AdminIdempotentService) LockRoster(
	ctx context.Context,
	command LockRosterCommand,
) (RosterView, error) {
	return executeAdminMutation(ctx, service, validLockRosterCommand(command), func() (idempotency.Command, error) {
		return lockRosterReceipt(command)
	}, func() (RosterView, error) {
		return service.next.LockRoster(ctx, command)
	})
}

func (service *AdminIdempotentService) UnlockRoster(
	ctx context.Context,
	command UnlockRosterCommand,
) (RosterView, error) {
	return executeAdminMutation(ctx, service, validUnlockRosterCommand(command), func() (idempotency.Command, error) {
		return unlockRosterReceipt(command)
	}, func() (RosterView, error) {
		return service.next.UnlockRoster(ctx, command)
	})
}

func (service *AdminIdempotentService) ConfigurePairings(
	ctx context.Context,
	command PairingCommand,
) (SwissRoundView, error) {
	return executeAdminMutation(ctx, service, validPairingCommand(command), func() (idempotency.Command, error) {
		return pairingReceipt(command)
	}, func() (SwissRoundView, error) {
		return service.next.ConfigurePairings(ctx, command)
	})
}

func (service *AdminIdempotentService) ApplyTournamentAction(
	ctx context.Context,
	command TournamentActionCommand,
) (usecase.TournamentView, error) {
	return executeAdminMutation(ctx, service, validTournamentActionCommand(command), func() (idempotency.Command, error) {
		return tournamentActionReceipt(command)
	}, func() (usecase.TournamentView, error) {
		return service.next.ApplyTournamentAction(ctx, command)
	})
}

func (service *AdminIdempotentService) ControlWave(ctx context.Context, command WaveCommand) (WaveView, error) {
	return executeAdminMutation(ctx, service, validWaveCommand(command), func() (idempotency.Command, error) {
		return waveReceipt(command)
	}, func() (WaveView, error) {
		return service.next.ControlWave(ctx, command)
	})
}

func (service *AdminIdempotentService) ResolveNoShow(ctx context.Context, command NoShowCommand) error {
	_, err := executeAdminMutation(ctx, service, validNoShowCommand(command), func() (idempotency.Command, error) {
		return noShowReceipt(command)
	}, func() (struct{}, error) {
		return struct{}{}, service.next.ResolveNoShow(ctx, command)
	})
	return err
}

func (service *AdminIdempotentService) AssignReserve(ctx context.Context, command ReserveCommand) error {
	_, err := executeAdminMutation(ctx, service, validReserveCommand(command), func() (idempotency.Command, error) {
		return reserveReceipt(command)
	}, func() (struct{}, error) {
		return struct{}{}, service.next.AssignReserve(ctx, command)
	})
	return err
}

func (service *AdminIdempotentService) RecordForfeit(ctx context.Context, command ForfeitCommand) error {
	_, err := executeAdminMutation(ctx, service, validForfeitCommand(command), func() (idempotency.Command, error) {
		return forfeitReceipt(command)
	}, func() (struct{}, error) {
		return struct{}{}, service.next.RecordForfeit(ctx, command)
	})
	return err
}

func (service *AdminIdempotentService) ReplayGame(ctx context.Context, command ReplayCommand) error {
	_, err := executeAdminMutation(ctx, service, validReplayCommand(command), func() (idempotency.Command, error) {
		return replayReceipt(command)
	}, func() (struct{}, error) {
		return struct{}{}, service.next.ReplayGame(ctx, command)
	})
	return err
}

func (service *AdminIdempotentService) CorrectGameResult(
	ctx context.Context,
	command CorrectionCommand,
) (CorrectionEvidence, error) {
	return executeAdminMutation(ctx, service, validCorrectionCommand(command), func() (idempotency.Command, error) {
		return correctionReceipt(command)
	}, func() (CorrectionEvidence, error) {
		return service.next.CorrectGameResult(ctx, command)
	})
}

func (service *AdminIdempotentService) ListAudit(ctx context.Context, query AuditQuery) (audit.AuditPage, error) {
	if service == nil || service.next == nil {
		return audit.AuditPage{}, domain.ErrInternal
	}
	return service.next.ListAudit(ctx, query)
}

func (service *AdminIdempotentService) ExportIncident(ctx context.Context, query IncidentQuery) (audit.IncidentBundle, error) {
	if service == nil || service.next == nil {
		return audit.IncidentBundle{}, domain.ErrInternal
	}
	return service.next.ExportIncident(ctx, query)
}

func (service *AdminIdempotentService) GetOperatorSnapshot(
	ctx context.Context,
	query SnapshotQuery,
) (OperatorSnapshotView, error) {
	if service == nil || service.next == nil {
		return OperatorSnapshotView{}, domain.ErrInternal
	}
	return service.next.GetOperatorSnapshot(ctx, query)
}

// CreateTournament keeps its existing tournament-create receipt boundary.
func (service *AdminIdempotentService) ListTournaments(
	ctx context.Context,
	command usecase.TournamentListCommand,
) (usecase.TournamentPage, error) {
	if service == nil || service.catalog == nil {
		return usecase.TournamentPage{}, domain.ErrInternal
	}
	return service.catalog.ListTournaments(ctx, command)
}

func (service *AdminIdempotentService) CreateTournament(
	ctx context.Context,
	command usecase.TournamentCreateCommand,
) (usecase.TournamentResult, error) {
	if service == nil || service.catalog == nil {
		return usecase.TournamentResult{}, domain.ErrInternal
	}
	return service.catalog.CreateTournament(ctx, command)
}

func (service *AdminIdempotentService) GetTournamentContent(
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
	service *AdminIdempotentService,
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

var _ AdminService = (*AdminIdempotentService)(nil)
var _ usecase.TournamentUseCase = (*AdminIdempotentService)(nil)
