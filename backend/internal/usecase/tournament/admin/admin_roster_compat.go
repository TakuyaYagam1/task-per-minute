package admin

import (
	"time"

	"github.com/google/uuid"

	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
)

type OperatorIdentity = operationusecase.OperatorIdentity
type RevisionConflictError = operationusecase.RevisionConflictError
type CommandScope = operationusecase.CommandScope

type RosterQuery = rosterusecase.RosterQuery
type RosterParticipantInput = rosterusecase.RosterParticipantInput
type ReplaceRosterCommand = rosterusecase.ReplaceRosterCommand
type PreflightCommand = rosterusecase.PreflightCommand
type LockRosterCommand = rosterusecase.LockRosterCommand
type UnlockRosterCommand = rosterusecase.UnlockRosterCommand
type RosterParticipantView = rosterusecase.RosterParticipantView
type RosterView = rosterusecase.RosterView
type RosterPort = rosterusecase.RosterPort
type PreflightPort = rosterusecase.PreflightPort

type RosterTransactionManager = rosterusecase.RosterTransactionManager
type RosterAuthority = rosterusecase.RosterAuthority
type RosterOperationAction = rosterusecase.RosterOperationAction
type RosterOperationRecord = rosterusecase.RosterOperationRecord
type RosterWorkflowRepository = rosterusecase.RosterWorkflowRepository
type PreflightRuntimeHealthSource = rosterusecase.PreflightRuntimeHealthSource
type PreflightRuntimeHealthSourceFunc = rosterusecase.PreflightRuntimeHealthSourceFunc
type RosterWorkflowDependencies = rosterusecase.RosterWorkflowDependencies
type RosterWorkflow = rosterusecase.RosterWorkflow

const (
	RosterOperationReplace   = rosterusecase.RosterOperationReplace
	RosterOperationPreflight = rosterusecase.RosterOperationPreflight
	RosterOperationLock      = rosterusecase.RosterOperationLock
	RosterOperationUnlock    = rosterusecase.RosterOperationUnlock
)

func NewReplaceRosterCommand(
	operator OperatorIdentity,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
	expectedRevision int64,
	participants []RosterParticipantInput,
) ReplaceRosterCommand {
	return rosterusecase.NewReplaceRosterCommand(
		operator, tournamentID, commandID, expectedRevision, participants,
	)
}

func NewRosterWorkflow(deps RosterWorkflowDependencies) *RosterWorkflow {
	return rosterusecase.NewRosterWorkflow(deps)
}

func validRosterQuery(query RosterQuery) bool {
	return rosterusecase.ValidRosterQuery(query)
}

func validReplaceRosterCommand(command ReplaceRosterCommand) bool {
	return rosterusecase.ValidReplaceRosterCommand(command)
}

func validPreflightCommand(command PreflightCommand) bool {
	return rosterusecase.ValidPreflightCommand(command)
}

func validLockRosterCommand(command LockRosterCommand) bool {
	return rosterusecase.ValidLockRosterCommand(command)
}

func validUnlockRosterCommand(command UnlockRosterCommand) bool {
	return rosterusecase.ValidUnlockRosterCommand(command)
}

func validRosterView(view RosterView, tournamentID uuid.UUID) bool {
	return rosterusecase.ValidRosterView(view, tournamentID)
}

func rosterRequestDigest(action RosterOperationAction, command any) ([32]byte, error) {
	return rosterusecase.RosterRequestDigest(action, command)
}

func cloneRosterView(view RosterView) RosterView {
	return rosterusecase.CloneRosterView(view)
}

type rosterOperationEvidence struct {
	preflightRevisionID uuid.UUID
	checkedInPlayerIDs  []uuid.UUID
}

func newRosterOperationRecord(
	scope CommandScope,
	action RosterOperationAction,
	authority RosterAuthority,
	resultingRosterRevision int64,
	digest [32]byte,
	evidence rosterOperationEvidence,
	result any,
	executedAt time.Time,
) (RosterOperationRecord, error) {
	return rosterusecase.NewRosterOperationRecord(
		scope, action, authority, resultingRosterRevision, digest,
		evidence.preflightRevisionID, evidence.checkedInPlayerIDs, result, executedAt,
	)
}

func validRosterOperationRecord(record RosterOperationRecord) bool {
	return rosterusecase.ValidRosterOperationRecord(record)
}
