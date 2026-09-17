package admin

import (
	"context"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	incidentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

type AdminDependencies struct {
	Catalog    usecase.TournamentUseCase
	Roster     RosterPort
	Preflight  PreflightPort
	Pairing    PairingPort
	Lifecycle  LifecyclePort
	Wave       WavePort
	NoShow     NoShowPort
	Reserve    ReservePort
	Forfeit    ForfeitPort
	Replay     ReplayPort
	Correction CorrectionPort
	Audit      incidentusecase.AuditPort
	Incidents  incidentusecase.IncidentSnapshotPort
	Signer     incidentusecase.IncidentAuthenticator
	Snapshots  SnapshotPort
}

type AdminUseCase struct {
	catalog    usecase.TournamentUseCase
	roster     RosterPort
	preflight  PreflightPort
	pairing    PairingPort
	lifecycle  LifecyclePort
	wave       WavePort
	noShow     NoShowPort
	reserve    ReservePort
	forfeit    ForfeitPort
	replay     ReplayPort
	correction CorrectionPort
	audit      incidentusecase.AuditPort
	incidents  incidentusecase.IncidentSnapshotPort
	signer     incidentusecase.IncidentAuthenticator
	snapshots  SnapshotPort
}

func AdminNewUseCase(deps AdminDependencies) *AdminUseCase {
	return &AdminUseCase{
		catalog: deps.Catalog, roster: deps.Roster, preflight: deps.Preflight, pairing: deps.Pairing,
		lifecycle: deps.Lifecycle, wave: deps.Wave, noShow: deps.NoShow,
		reserve: deps.Reserve, forfeit: deps.Forfeit, replay: deps.Replay,
		correction: deps.Correction, audit: deps.Audit, incidents: deps.Incidents,
		signer: deps.Signer, snapshots: deps.Snapshots,
	}
}

func (a *AdminUseCase) GetRoster(ctx context.Context, query RosterQuery) (RosterView, error) {
	if ctx == nil || !validRosterQuery(query) {
		return RosterView{}, domain.ErrValidation
	}
	if a == nil || a.roster == nil {
		return RosterView{}, domain.ErrInternal
	}
	view, err := a.roster.GetRoster(ctx, query)
	if err != nil {
		return RosterView{}, normalizeAdminError(err)
	}
	if !validRosterView(view, query.TournamentID) {
		return RosterView{}, domain.ErrInternal
	}
	return view, nil
}

func (a *AdminUseCase) ReplaceRoster(ctx context.Context, command ReplaceRosterCommand) (RosterView, error) {
	return a.rosterMutation(ctx, command.CommandScope, validReplaceRosterCommand(command), func() (RosterView, error) {
		return a.roster.ReplaceRoster(ctx, command)
	})
}

func (a *AdminUseCase) LockRoster(ctx context.Context, command LockRosterCommand) (RosterView, error) {
	return a.rosterMutation(ctx, command.CommandScope, validLockRosterCommand(command), func() (RosterView, error) {
		return a.roster.LockRoster(ctx, command)
	})
}

func (a *AdminUseCase) UnlockRoster(ctx context.Context, command UnlockRosterCommand) (RosterView, error) {
	return a.rosterMutation(ctx, command.CommandScope, validUnlockRosterCommand(command), func() (RosterView, error) {
		return a.roster.UnlockRoster(ctx, command)
	})
}

func (a *AdminUseCase) rosterMutation(
	ctx context.Context,
	scope CommandScope,
	valid bool,
	invoke func() (RosterView, error),
) (RosterView, error) {
	if ctx == nil || !valid {
		return RosterView{}, domain.ErrValidation
	}
	if a == nil || a.roster == nil {
		return RosterView{}, domain.ErrInternal
	}
	view, err := invoke()
	if err != nil {
		return RosterView{}, normalizeAdminError(err)
	}
	if !validRosterView(view, scope.TournamentID) {
		return RosterView{}, domain.ErrInternal
	}
	return view, nil
}

func (a *AdminUseCase) RunPreflight(
	ctx context.Context,
	command PreflightCommand,
) (tournamentpreflight.ReportRevision, error) {
	if ctx == nil || !validPreflightCommand(command) {
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
	command PairingCommand,
) (SwissRoundView, error) {
	if ctx == nil || !validPairingCommand(command) {
		return SwissRoundView{}, domain.ErrValidation
	}
	if a == nil || a.pairing == nil {
		return SwissRoundView{}, domain.ErrInternal
	}
	view, err := a.pairing.ConfigurePairings(ctx, command)
	if err != nil {
		return SwissRoundView{}, normalizeAdminError(err)
	}
	if !validSwissRoundView(view, command.TournamentID, command.RoundNumber) {
		return SwissRoundView{}, fmt.Errorf("validate pairing response: %w", domain.ErrInternal)
	}
	return view, nil
}

func (a *AdminUseCase) ApplyTournamentAction(
	ctx context.Context,
	command TournamentActionCommand,
) (usecase.TournamentView, error) {
	if ctx == nil || !validTournamentActionCommand(command) {
		return usecase.TournamentView{}, domain.ErrValidation
	}
	if a == nil || a.lifecycle == nil {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	view, err := a.lifecycle.ApplyTournamentAction(ctx, command)
	if err != nil {
		return usecase.TournamentView{}, normalizeAdminError(err)
	}
	if !validTournamentView(view, command.TournamentID) {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	return view, nil
}

func (a *AdminUseCase) ControlWave(ctx context.Context, command WaveCommand) (WaveView, error) {
	if ctx == nil || !validWaveCommand(command) {
		return WaveView{}, domain.ErrValidation
	}
	if a == nil || a.wave == nil {
		return WaveView{}, domain.ErrInternal
	}
	view, err := a.wave.ControlWave(ctx, command)
	if err != nil {
		return WaveView{}, normalizeAdminError(err)
	}
	if !validWaveView(view, command.TournamentID, command.WaveID) {
		return WaveView{}, domain.ErrInternal
	}
	return view, nil
}

func (a *AdminUseCase) ResolveNoShow(ctx context.Context, command NoShowCommand) error {
	if ctx == nil || !validNoShowCommand(command) {
		return domain.ErrValidation
	}
	if a == nil || a.noShow == nil {
		return domain.ErrInternal
	}
	return normalizeAdminError(a.noShow.ResolveNoShow(ctx, command))
}

func (a *AdminUseCase) AssignReserve(ctx context.Context, command ReserveCommand) error {
	if ctx == nil || !validReserveCommand(command) {
		return domain.ErrValidation
	}
	if a == nil || a.reserve == nil {
		return domain.ErrInternal
	}
	return normalizeAdminError(a.reserve.AssignReserve(ctx, command))
}

func (a *AdminUseCase) RecordForfeit(ctx context.Context, command ForfeitCommand) error {
	if ctx == nil || !validForfeitCommand(command) {
		return domain.ErrValidation
	}
	if a == nil || a.forfeit == nil {
		return domain.ErrInternal
	}
	return normalizeAdminError(a.forfeit.RecordForfeit(ctx, command))
}

func (a *AdminUseCase) ReplayGame(ctx context.Context, command ReplayCommand) error {
	if ctx == nil || !validReplayCommand(command) {
		return domain.ErrValidation
	}
	if a == nil || a.replay == nil {
		return domain.ErrInternal
	}
	return normalizeAdminError(a.replay.ReplayGame(ctx, command))
}

func (a *AdminUseCase) CorrectGameResult(
	ctx context.Context,
	command CorrectionCommand,
) (CorrectionEvidence, error) {
	if ctx == nil || !validCorrectionCommand(command) {
		return CorrectionEvidence{}, domain.ErrValidation
	}
	if a == nil || a.correction == nil {
		return CorrectionEvidence{}, domain.ErrInternal
	}
	evidence, err := a.correction.CorrectGameResult(ctx, command)
	if err != nil {
		return CorrectionEvidence{}, normalizeAdminError(err)
	}
	if !validCorrectionEvidence(evidence, command) {
		return CorrectionEvidence{}, domain.ErrInternal
	}
	return evidence, nil
}

func (a *AdminUseCase) ListAudit(ctx context.Context, query AuditQuery) (audit.AuditPage, error) {
	if ctx == nil || !validAuditQuery(query) {
		return audit.AuditPage{}, domain.ErrValidation
	}
	if a == nil || a.audit == nil {
		return audit.AuditPage{}, domain.ErrInternal
	}
	page, err := a.audit.ListAudit(ctx, query)
	if err != nil {
		return audit.AuditPage{}, normalizeAdminError(err)
	}
	if !validAuditPage(page, query.Filter.TournamentID) {
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
	query SnapshotQuery,
) (OperatorSnapshotView, error) {
	if ctx == nil || !validSnapshotQuery(query) {
		return OperatorSnapshotView{}, domain.ErrValidation
	}
	if a == nil || a.snapshots == nil {
		return OperatorSnapshotView{}, domain.ErrInternal
	}
	view, err := a.snapshots.GetOperatorSnapshot(ctx, query)
	if err != nil {
		return OperatorSnapshotView{}, normalizeAdminError(err)
	}
	if !validOperatorSnapshot(view, query.TournamentID) {
		return OperatorSnapshotView{}, domain.ErrInternal
	}
	return view, nil
}

var _ AdminService = (*AdminUseCase)(nil)
