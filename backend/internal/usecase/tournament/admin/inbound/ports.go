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
type OperatorSnapshotView = snapshot.OperatorSnapshotView

type AdminService interface {
	GetRoster(context.Context, RosterQuery) (RosterView, error)
	ReplaceRoster(context.Context, ReplaceRosterCommand) (RosterView, error)
	RunPreflight(context.Context, PreflightCommand) (preflight.ReportRevision, error)
	LockRoster(context.Context, LockRosterCommand) (RosterView, error)
	UnlockRoster(context.Context, UnlockRosterCommand) (RosterView, error)
	ConfigurePairings(context.Context, PairingCommand) (SwissRoundView, error)
	ApplyTournamentAction(context.Context, TournamentActionCommand) (contract.TournamentView, error)
	ControlWave(context.Context, WaveCommand) (WaveView, error)
	ResolveNoShow(context.Context, NoShowCommand) error
	AssignReserve(context.Context, ReserveCommand) error
	RecordForfeit(context.Context, ForfeitCommand) error
	ReplayGame(context.Context, ReplayCommand) error
	CorrectGameResult(context.Context, CorrectionCommand) (CorrectionEvidence, error)
	ListAudit(context.Context, AuditQuery) (audit.AuditPage, error)
	ExportIncident(context.Context, IncidentQuery) (audit.IncidentBundle, error)
	GetOperatorSnapshot(context.Context, SnapshotQuery) (OperatorSnapshotView, error)
}

type Service = AdminService
