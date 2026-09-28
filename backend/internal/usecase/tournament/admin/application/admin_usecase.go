package application

import (
	"context"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	executionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	admininbound "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	snapshotusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

type AdminDependencies struct {
	Catalog    usecase.TournamentUseCase
	Roster     rosterusecase.RosterPort
	Preflight  rosterusecase.PreflightPort
	Pairing    executionusecase.PairingPort
	Lifecycle  lifecycleusecase.LifecyclePort
	Deletion   lifecycleusecase.DeletionPort
	Wave       executionusecase.WavePort
	NoShow     resultusecase.NoShowPort
	Reserve    replayusecase.ReservePort
	Forfeit    resultusecase.ForfeitPort
	Replay     replayusecase.ReplayPort
	Correction correctionusecase.CorrectionPort
	Audit      incidentusecase.AuditPort
	Incidents  incidentusecase.IncidentSnapshotPort
	Signer     incidentusecase.IncidentAuthenticator
	Snapshots  snapshotusecase.SnapshotPort
}

type AdminUseCase struct {
	catalog    usecase.TournamentUseCase
	roster     rosterusecase.RosterPort
	preflight  rosterusecase.PreflightPort
	pairing    executionusecase.PairingPort
	lifecycle  lifecycleusecase.LifecyclePort
	deletion   lifecycleusecase.DeletionPort
	wave       executionusecase.WavePort
	noShow     resultusecase.NoShowPort
	reserve    replayusecase.ReservePort
	forfeit    resultusecase.ForfeitPort
	replay     replayusecase.ReplayPort
	correction correctionusecase.CorrectionPort
	audit      incidentusecase.AuditPort
	incidents  incidentusecase.IncidentSnapshotPort
	signer     incidentusecase.IncidentAuthenticator
	snapshots  snapshotusecase.SnapshotPort
}

func AdminNewUseCase(deps AdminDependencies) *AdminUseCase {
	return &AdminUseCase{
		catalog: deps.Catalog, roster: deps.Roster, preflight: deps.Preflight, pairing: deps.Pairing,
		lifecycle: deps.Lifecycle, deletion: deps.Deletion, wave: deps.Wave, noShow: deps.NoShow,
		reserve: deps.Reserve, forfeit: deps.Forfeit, replay: deps.Replay,
		correction: deps.Correction, audit: deps.Audit, incidents: deps.Incidents,
		signer: deps.Signer, snapshots: deps.Snapshots,
	}
}

func (a *AdminUseCase) GetRoster(ctx context.Context, query rosterusecase.RosterQuery) (rosterusecase.RosterView, error) {
	if ctx == nil || !rosterusecase.ValidRosterQuery(query) {
		return rosterusecase.RosterView{}, domain.ErrValidation
	}
	if a == nil || a.roster == nil {
		return rosterusecase.RosterView{}, domain.ErrInternal
	}
	view, err := a.roster.GetRoster(ctx, query)
	if err != nil {
		return rosterusecase.RosterView{}, normalizeAdminError(err)
	}
	if !rosterusecase.ValidRosterView(view, query.TournamentID) {
		return rosterusecase.RosterView{}, domain.ErrInternal
	}
	return view, nil
}

func (a *AdminUseCase) ReplaceRoster(ctx context.Context, command rosterusecase.ReplaceRosterCommand) (rosterusecase.RosterView, error) {
	return a.rosterMutation(ctx, command.CommandScope, rosterusecase.ValidReplaceRosterCommand(command), func() (rosterusecase.RosterView, error) {
		return a.roster.ReplaceRoster(ctx, command)
	})
}

func (a *AdminUseCase) LockRoster(ctx context.Context, command rosterusecase.LockRosterCommand) (rosterusecase.RosterView, error) {
	return a.rosterMutation(ctx, command.CommandScope, rosterusecase.ValidLockRosterCommand(command), func() (rosterusecase.RosterView, error) {
		return a.roster.LockRoster(ctx, command)
	})
}

func (a *AdminUseCase) UnlockRoster(ctx context.Context, command rosterusecase.UnlockRosterCommand) (rosterusecase.RosterView, error) {
	return a.rosterMutation(ctx, command.CommandScope, rosterusecase.ValidUnlockRosterCommand(command), func() (rosterusecase.RosterView, error) {
		return a.roster.UnlockRoster(ctx, command)
	})
}

func (a *AdminUseCase) rosterMutation(
	ctx context.Context,
	scope operationusecase.CommandScope,
	valid bool,
	invoke func() (rosterusecase.RosterView, error),
) (rosterusecase.RosterView, error) {
	if ctx == nil || !valid {
		return rosterusecase.RosterView{}, domain.ErrValidation
	}
	if a == nil || a.roster == nil {
		return rosterusecase.RosterView{}, domain.ErrInternal
	}
	view, err := invoke()
	if err != nil {
		return rosterusecase.RosterView{}, normalizeAdminError(err)
	}
	if !rosterusecase.ValidRosterView(view, scope.TournamentID) {
		return rosterusecase.RosterView{}, domain.ErrInternal
	}
	return view, nil
}

func (a *AdminUseCase) RunPreflight(
	ctx context.Context,
	command rosterusecase.PreflightCommand,
) (tournamentpreflight.ReportRevision, error) {
	if ctx == nil || !rosterusecase.ValidPreflightCommand(command) {
		return tournamentpreflight.ReportRevision{}, domain.ErrValidation
	}
	if a == nil || a.preflight == nil {
		return tournamentpreflight.ReportRevision{}, domain.ErrInternal
	}
	report, err := a.preflight.RunPreflight(ctx, command)
	if err != nil {
		return tournamentpreflight.ReportRevision{}, normalizeAdminError(err)
	}
	if report.TournamentID != command.TournamentID || report.Validate() != nil {
		return tournamentpreflight.ReportRevision{}, domain.ErrInternal
	}
	return report, nil
}

func (a *AdminUseCase) ConfigurePairings(
	ctx context.Context,
	command pairingusecase.PairingCommand,
) (executionusecase.SwissRoundView, error) {
	if ctx == nil || !pairingusecase.ValidPairingCommand(command) {
		return executionusecase.SwissRoundView{}, domain.ErrValidation
	}
	if a == nil || a.pairing == nil {
		return executionusecase.SwissRoundView{}, domain.ErrInternal
	}
	view, err := a.pairing.ConfigurePairings(ctx, command)
	if err != nil {
		return executionusecase.SwissRoundView{}, normalizeAdminError(err)
	}
	if !executionusecase.ValidSwissRoundView(view, command.TournamentID, command.RoundNumber) {
		return executionusecase.SwissRoundView{}, fmt.Errorf("validate pairing response: %w", domain.ErrInternal)
	}
	return view, nil
}

func (a *AdminUseCase) ApplyTournamentAction(
	ctx context.Context,
	command lifecycleusecase.TournamentActionCommand,
) (usecase.TournamentView, error) {
	if ctx == nil || !lifecycleusecase.ValidTournamentActionCommand(command) {
		return usecase.TournamentView{}, domain.ErrValidation
	}
	if a == nil || a.lifecycle == nil {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	view, err := a.lifecycle.ApplyTournamentAction(ctx, command)
	if err != nil {
		return usecase.TournamentView{}, normalizeAdminError(err)
	}
	if !lifecycleusecase.ValidTournamentView(view, command.TournamentID) {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	return view, nil
}

func (a *AdminUseCase) DeleteTournament(
	ctx context.Context,
	command lifecycleusecase.TournamentDeletionCommand,
) (lifecycleusecase.TournamentDeletionRecord, error) {
	if ctx == nil || !lifecycleusecase.ValidTournamentDeletionCommand(command) {
		return lifecycleusecase.TournamentDeletionRecord{}, domain.ErrValidation
	}
	if a == nil || a.deletion == nil {
		return lifecycleusecase.TournamentDeletionRecord{}, domain.ErrInternal
	}
	record, err := a.deletion.DeleteTournament(ctx, command)
	if err != nil {
		return lifecycleusecase.TournamentDeletionRecord{}, normalizeAdminError(err)
	}
	if record.CommandID != command.CommandID || record.TournamentID != command.TournamentID ||
		record.ActorID != command.Operator.ActorID || record.SourceRevision != command.ExpectedRevision ||
		record.ResultingRevision <= record.SourceRevision || record.Reason != lifecycleusecase.TournamentDeletionReason {
		return lifecycleusecase.TournamentDeletionRecord{}, domain.ErrInternal
	}
	return record, nil
}

func (a *AdminUseCase) ControlWave(ctx context.Context, command executionusecase.WaveCommand) (executionusecase.WaveView, error) {
	if ctx == nil || !executionusecase.ValidWaveCommand(command) {
		return executionusecase.WaveView{}, domain.ErrValidation
	}
	if a == nil || a.wave == nil {
		return executionusecase.WaveView{}, domain.ErrInternal
	}
	view, err := a.wave.ControlWave(ctx, command)
	if err != nil {
		return executionusecase.WaveView{}, normalizeAdminError(err)
	}
	if !executionusecase.ValidWaveView(view, command.TournamentID, command.WaveID) {
		return executionusecase.WaveView{}, domain.ErrInternal
	}
	return view, nil
}

func (a *AdminUseCase) ResolveNoShow(ctx context.Context, command resultusecase.NoShowCommand) error {
	if ctx == nil || !resultusecase.ValidNoShowCommand(command) {
		return domain.ErrValidation
	}
	if a == nil || a.noShow == nil {
		return domain.ErrInternal
	}
	return normalizeAdminError(a.noShow.ResolveNoShow(ctx, command))
}

func (a *AdminUseCase) AssignReserve(ctx context.Context, command replayusecase.ReserveCommand) error {
	if ctx == nil || !replayusecase.ValidReserveCommand(command) {
		return domain.ErrValidation
	}
	if a == nil || a.reserve == nil {
		return domain.ErrInternal
	}
	return normalizeAdminError(a.reserve.AssignReserve(ctx, command))
}

func (a *AdminUseCase) RecordForfeit(ctx context.Context, command resultusecase.ForfeitCommand) error {
	if ctx == nil || !resultusecase.ValidForfeitCommand(command) {
		return domain.ErrValidation
	}
	if a == nil || a.forfeit == nil {
		return domain.ErrInternal
	}
	return normalizeAdminError(a.forfeit.RecordForfeit(ctx, command))
}

func (a *AdminUseCase) ReplayGame(ctx context.Context, command replayusecase.ReplayCommand) error {
	if ctx == nil || !replayusecase.ValidReplayCommand(command) {
		return domain.ErrValidation
	}
	if a == nil || a.replay == nil {
		return domain.ErrInternal
	}
	return normalizeAdminError(a.replay.ReplayGame(ctx, command))
}

func (a *AdminUseCase) CorrectGameResult(
	ctx context.Context,
	command correctionusecase.CorrectionCommand,
) (correctionusecase.CorrectionEvidence, error) {
	if ctx == nil || !correctionusecase.ValidCorrectionCommandWithSource(command) {
		return correctionusecase.CorrectionEvidence{}, domain.ErrValidation
	}
	if a == nil || a.correction == nil {
		return correctionusecase.CorrectionEvidence{}, domain.ErrInternal
	}
	evidence, err := a.correction.CorrectGameResult(ctx, command)
	if err != nil {
		return correctionusecase.CorrectionEvidence{}, normalizeAdminError(err)
	}
	if !correctionusecase.ValidCorrectionEvidence(evidence, command) {
		return correctionusecase.CorrectionEvidence{}, domain.ErrInternal
	}
	return evidence, nil
}

func (a *AdminUseCase) PrepareGameResultCorrection(
	ctx context.Context,
	command correctionusecase.CorrectionCommand,
) (correctionusecase.CorrectionCommand, error) {
	if ctx == nil || !correctionusecase.ValidCorrectionDraftCommand(command) {
		return correctionusecase.CorrectionCommand{}, domain.ErrValidation
	}
	if a == nil || a.correction == nil {
		return correctionusecase.CorrectionCommand{}, domain.ErrInternal
	}
	preparer, ok := a.correction.(interface {
		PrepareGameResultCorrection(ctx context.Context, command correctionusecase.CorrectionCommand) (correctionusecase.CorrectionCommand, error)
	})
	if !ok {
		return correctionusecase.CorrectionCommand{}, domain.ErrInternal
	}
	prepared, err := preparer.PrepareGameResultCorrection(ctx, command)
	if err != nil {
		return correctionusecase.CorrectionCommand{}, normalizeAdminError(err)
	}
	if !correctionusecase.ValidCorrectionCommandWithSource(prepared) {
		return correctionusecase.CorrectionCommand{}, domain.ErrInternal
	}
	return prepared, nil
}

func (a *AdminUseCase) ListAudit(ctx context.Context, query incidentusecase.AuditQuery) (audit.AuditPage, error) {
	if ctx == nil || !incidentusecase.ValidAuditQuery(query) {
		return audit.AuditPage{}, domain.ErrValidation
	}
	if a == nil || a.audit == nil {
		return audit.AuditPage{}, domain.ErrInternal
	}
	page, err := a.audit.ListAudit(ctx, query)
	if err != nil {
		return audit.AuditPage{}, normalizeAdminError(err)
	}
	if !incidentusecase.ValidAuditPage(page, query.Filter.TournamentID) {
		return audit.AuditPage{}, domain.ErrInternal
	}
	return page, nil
}

func (a *AdminUseCase) ExportIncident(
	ctx context.Context,
	query incidentusecase.IncidentQuery,
) (audit.IncidentBundle, error) {
	if ctx == nil || !incidentusecase.ValidIncidentQuery(query) {
		return audit.IncidentBundle{}, domain.ErrValidation
	}
	if a == nil || a.incidents == nil || a.signer == nil {
		return audit.IncidentBundle{}, domain.ErrInternal
	}
	snapshot, err := a.incidents.LoadIncidentSnapshot(ctx, query)
	if err != nil {
		return audit.IncidentBundle{}, normalizeAdminError(err)
	}
	if snapshot.TournamentID != query.TournamentID {
		return audit.IncidentBundle{}, domain.ErrInternal
	}
	bundle, err := audit.GenerateIncidentBundle(snapshot)
	if err != nil {
		return audit.IncidentBundle{}, domain.ErrInternal
	}
	bundle, err = a.signer.Sign(bundle)
	if err != nil || a.signer.Verify(bundle) != nil || audit.VerifyIncidentBundle(bundle) != nil ||
		audit.VerifyIncidentBundleAuthenticityEnvelope(bundle) != nil {
		return audit.IncidentBundle{}, domain.ErrInternal
	}
	return bundle, nil
}

func (a *AdminUseCase) GetOperatorSnapshot(
	ctx context.Context,
	query snapshotusecase.SnapshotQuery,
) (snapshotusecase.OperatorSnapshotView, error) {
	if ctx == nil || !snapshotusecase.ValidSnapshotQuery(query) {
		return snapshotusecase.OperatorSnapshotView{}, domain.ErrValidation
	}
	if a == nil || a.snapshots == nil {
		return snapshotusecase.OperatorSnapshotView{}, domain.ErrInternal
	}
	view, err := a.snapshots.GetOperatorSnapshot(ctx, query)
	if err != nil {
		return snapshotusecase.OperatorSnapshotView{}, normalizeAdminError(err)
	}
	if !snapshotusecase.ValidOperatorSnapshot(view, query.TournamentID) {
		return snapshotusecase.OperatorSnapshotView{}, domain.ErrInternal
	}
	return view, nil
}

var _ admininbound.AdminService = (*AdminUseCase)(nil)
