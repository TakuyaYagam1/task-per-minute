package inbound

import (
	"context"

	contract "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
	preflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

type OperatorIdentity = operation.OperatorIdentity
type RevisionConflictError = operation.RevisionConflictError
type CommandScope = operation.CommandScope

type RosterQuery = roster.RosterQuery
type RosterParticipantInput = roster.RosterParticipantInput
type ReplaceRosterCommand = roster.ReplaceRosterCommand
type PreflightCommand = roster.PreflightCommand
type LockRosterCommand = roster.LockRosterCommand
type UnlockRosterCommand = roster.UnlockRosterCommand
type RosterParticipantView = roster.RosterParticipantView
type RosterView = roster.RosterView

type PairingCommand = pairing.PairingCommand
type PairingMode = pairing.PairingMode
type ParticipantPair = pairing.ParticipantPair

type TournamentActionCommand = lifecycle.TournamentActionCommand
type TournamentAction = lifecycle.TournamentAction
type TournamentDeletionCommand = lifecycle.TournamentDeletionCommand
type TournamentDeletionRecord = lifecycle.TournamentDeletionRecord
type WaveCommand = execution.WaveCommand
type WaveAction = execution.WaveAction
type WaveView = execution.WaveView
type SwissRoundView = execution.SwissRoundView

type NoShowCommand = result.NoShowCommand
type GameExpectation = result.GameExpectation
type ForfeitCommand = result.ForfeitCommand
type ReserveCommand = replay.ReserveCommand
type ReplayCommand = replay.ReplayCommand

type ProjectionRevisionExpectation = correction.ProjectionRevisionExpectation
type CorrectionProjectionIntent = correction.CorrectionProjectionIntent
type CorrectionUnlockIntent = correction.CorrectionUnlockIntent
type CorrectionPatch = correction.CorrectionPatch
type CorrectionCommand = correction.CorrectionCommand
type ProjectionSupersessionView = correction.ProjectionSupersessionView
type CorrectionEvidence = correction.CorrectionEvidence

type AuditQuery = incident.AuditQuery
type IncidentQuery = incident.IncidentQuery
type SnapshotQuery = snapshot.SnapshotQuery
type OperatorCursor = snapshot.OperatorCursor
type PauseGraphView = snapshot.PauseGraphView
type RecoveryControlKind = snapshot.RecoveryControlKind
type RecoveryReplayDetails = snapshot.RecoveryReplayDetails
type RecoveryReserveCandidate = snapshot.RecoveryReserveCandidate
type RecoveryReserveExhaustedDetails = snapshot.RecoveryReserveExhaustedDetails
type RecoveryControl = snapshot.RecoveryControl
type OperatorSnapshotView = snapshot.OperatorSnapshotView

type AdminService interface {
	GetRoster(ctx context.Context, query RosterQuery) (RosterView, error)
	ReplaceRoster(ctx context.Context, command ReplaceRosterCommand) (RosterView, error)
	RunPreflight(ctx context.Context, command PreflightCommand) (preflight.ReportRevision, error)
	LockRoster(ctx context.Context, command LockRosterCommand) (RosterView, error)
	UnlockRoster(ctx context.Context, command UnlockRosterCommand) (RosterView, error)
	ConfigurePairings(ctx context.Context, command PairingCommand) (SwissRoundView, error)
	ApplyTournamentAction(ctx context.Context, command TournamentActionCommand) (contract.TournamentView, error)
	DeleteTournament(ctx context.Context, command TournamentDeletionCommand) (TournamentDeletionRecord, error)
	ControlWave(ctx context.Context, command WaveCommand) (WaveView, error)
	ResolveNoShow(ctx context.Context, command NoShowCommand) error
	AssignReserve(ctx context.Context, command ReserveCommand) error
	RecordForfeit(ctx context.Context, command ForfeitCommand) error
	ReplayGame(ctx context.Context, command ReplayCommand) error
	CorrectGameResult(ctx context.Context, command CorrectionCommand) (CorrectionEvidence, error)
	ListAudit(ctx context.Context, query AuditQuery) (audit.AuditPage, error)
	ExportIncident(ctx context.Context, query IncidentQuery) (audit.IncidentBundle, error)
	GetOperatorSnapshot(ctx context.Context, query SnapshotQuery) (OperatorSnapshotView, error)
}

type Service = AdminService
