package idempotent

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
	executionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
)

const (
	adminReplaceRosterReceiptNamespace      = "admin-roster-replace"
	adminRunPreflightReceiptNamespace       = "admin-preflight-run"
	adminLockRosterReceiptNamespace         = "admin-roster-lock"
	adminUnlockRosterReceiptNamespace       = "admin-roster-unlock"
	adminPairingReceiptNamespace            = "admin-pairing-configure"
	adminTournamentActionReceiptNamespace   = "admin-tournament-action"
	adminTournamentDeletionReceiptNamespace = "admin-tournament-delete"
	adminWaveReceiptNamespace               = "admin-wave-control"
	adminNoShowReceiptNamespace             = "admin-no-show-resolve"
	adminReserveReceiptNamespace            = "admin-reserve-assign"
	adminForfeitReceiptNamespace            = "admin-forfeit-record"
	adminReplayReceiptNamespace             = "admin-game-replay"
	adminCorrectionReceiptNamespace         = "admin-result-correct"
)

const tournamentActionReceiptVersion = "tournament-admin-lifecycle-receipt-v1"
const tournamentDeletionReceiptVersion = "tournament-admin-deletion-receipt-v1"

func replaceRosterReceipt(command rosterusecase.ReplaceRosterCommand) (idempotency.Command, error) {
	return rosterReceipt(adminReplaceRosterReceiptNamespace, rosterusecase.RosterOperationReplace, command.CommandID, command)
}

func preflightReceipt(command rosterusecase.PreflightCommand) (idempotency.Command, error) {
	return rosterReceipt(adminRunPreflightReceiptNamespace, rosterusecase.RosterOperationPreflight, command.CommandID, command)
}

func lockRosterReceipt(command rosterusecase.LockRosterCommand) (idempotency.Command, error) {
	return rosterReceipt(adminLockRosterReceiptNamespace, rosterusecase.RosterOperationLock, command.CommandID, command)
}

func unlockRosterReceipt(command rosterusecase.UnlockRosterCommand) (idempotency.Command, error) {
	return rosterReceipt(adminUnlockRosterReceiptNamespace, rosterusecase.RosterOperationUnlock, command.CommandID, command)
}

func rosterReceipt(
	namespace string,
	action rosterusecase.RosterOperationAction,
	commandID uuid.UUID,
	command any,
) (idempotency.Command, error) {
	digest, err := rosterusecase.RosterRequestDigest(action, command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(namespace, commandID, digest)
}

func pairingReceipt(command pairingusecase.PairingCommand) (idempotency.Command, error) {
	return executionReceipt(adminPairingReceiptNamespace, command.CommandID, command)
}

func waveReceipt(command executionusecase.WaveCommand) (idempotency.Command, error) {
	return executionReceipt(adminWaveReceiptNamespace, command.CommandID, command)
}

func executionReceipt(namespace string, commandID uuid.UUID, command any) (idempotency.Command, error) {
	digest, err := executionusecase.ExecutionRequestDigest(command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(namespace, commandID, digest)
}

func tournamentActionReceipt(command lifecycleusecase.TournamentActionCommand) (idempotency.Command, error) {
	return adminReceipt(
		adminTournamentActionReceiptNamespace,
		command.CommandID,
		tournamentActionReceiptDigest(command),
	)
}

func tournamentDeletionReceipt(command lifecycleusecase.TournamentDeletionCommand) (idempotency.Command, error) {
	return adminReceipt(
		adminTournamentDeletionReceiptNamespace,
		command.CommandID,
		tournamentDeletionReceiptDigest(command),
	)
}

func noShowReceipt(command resultusecase.NoShowCommand) (idempotency.Command, error) {
	return operatorResultReceipt(adminNoShowReceiptNamespace, resultusecase.OperatorResultActionNoShow, command.CommandID, command)
}

func forfeitReceipt(command resultusecase.ForfeitCommand) (idempotency.Command, error) {
	return operatorResultReceipt(adminForfeitReceiptNamespace, resultusecase.OperatorResultActionForfeit, command.CommandID, command)
}

func operatorResultReceipt(
	namespace string,
	action resultusecase.OperatorResultAction,
	commandID uuid.UUID,
	command any,
) (idempotency.Command, error) {
	digest, err := resultusecase.OperatorResultDigest(action, command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(namespace, commandID, digest)
}

func reserveReceipt(command replayusecase.ReserveCommand) (idempotency.Command, error) {
	return replayWorkflowReceipt(adminReserveReceiptNamespace, "operator_reserve", command.CommandID, command)
}

func replayReceipt(command replayusecase.ReplayCommand) (idempotency.Command, error) {
	return replayWorkflowReceipt(adminReplayReceiptNamespace, "replacement", command.CommandID, command)
}

func replayWorkflowReceipt(
	namespace string,
	action string,
	commandID uuid.UUID,
	command any,
) (idempotency.Command, error) {
	digest, err := replayusecase.ReplayWorkflowDigest(action, command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(namespace, commandID, digest)
}

func correctionReceipt(command correctionusecase.CorrectionCommand) (idempotency.Command, error) {
	digest, err := correctionusecase.CorrectionRequestDigest(command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(adminCorrectionReceiptNamespace, command.CommandID, digest)
}

func adminReceipt(namespace string, commandID uuid.UUID, digest [sha256.Size]byte) (idempotency.Command, error) {
	return idempotency.NewCommand(namespace, commandID, digest)
}

func tournamentActionReceiptDigest(command lifecycleusecase.TournamentActionCommand) [sha256.Size]byte {
	var payload bytes.Buffer
	writeReceiptString(&payload, tournamentActionReceiptVersion)
	writeReceiptString(&payload, adminTournamentActionReceiptNamespace)
	writeReceiptUUID(&payload, command.Operator.ActorID)
	writeReceiptUUID(&payload, command.TournamentID)
	writeReceiptInt64(&payload, command.ExpectedProjectionRevision)
	writeReceiptString(&payload, string(command.Action))
	if command.Confirmed {
		payload.WriteByte(1)
	} else {
		payload.WriteByte(0)
	}
	writeReceiptString(&payload, command.Reason)
	return sha256.Sum256(payload.Bytes())
}

func tournamentDeletionReceiptDigest(command lifecycleusecase.TournamentDeletionCommand) [sha256.Size]byte {
	var payload bytes.Buffer
	writeReceiptString(&payload, tournamentDeletionReceiptVersion)
	writeReceiptString(&payload, adminTournamentDeletionReceiptNamespace)
	writeReceiptUUID(&payload, command.Operator.ActorID)
	writeReceiptUUID(&payload, command.TournamentID)
	writeReceiptInt64(&payload, command.ExpectedRevision)
	if command.Confirmed {
		payload.WriteByte(1)
	} else {
		payload.WriteByte(0)
	}
	return sha256.Sum256(payload.Bytes())
}

func writeReceiptUUID(payload *bytes.Buffer, value uuid.UUID) {
	_, _ = payload.Write(value[:])
}

func writeReceiptInt64(payload *bytes.Buffer, value int64) {
	var encoded [8]byte
	//nolint:gosec // Signed timestamp bits are intentionally encoded as unsigned digest input.
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = payload.Write(encoded[:])
}

func writeReceiptString(payload *bytes.Buffer, value string) {
	writeReceiptInt64(payload, int64(len(value)))
	_, _ = payload.WriteString(value)
}
