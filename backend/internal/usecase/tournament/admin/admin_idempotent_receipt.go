package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

const tournamentActionReceiptVersion = "tournament-admin-lifecycle-receipt-v1"

func replaceRosterReceipt(command ReplaceRosterCommand) (idempotency.Command, error) {
	return rosterReceipt(adminReplaceRosterReceiptNamespace, RosterOperationReplace, command.CommandID, command)
}

func preflightReceipt(command PreflightCommand) (idempotency.Command, error) {
	return rosterReceipt(adminRunPreflightReceiptNamespace, RosterOperationPreflight, command.CommandID, command)
}

func lockRosterReceipt(command LockRosterCommand) (idempotency.Command, error) {
	return rosterReceipt(adminLockRosterReceiptNamespace, RosterOperationLock, command.CommandID, command)
}

func unlockRosterReceipt(command UnlockRosterCommand) (idempotency.Command, error) {
	return rosterReceipt(adminUnlockRosterReceiptNamespace, RosterOperationUnlock, command.CommandID, command)
}

func rosterReceipt(
	namespace string,
	action RosterOperationAction,
	commandID uuid.UUID,
	command any,
) (idempotency.Command, error) {
	digest, err := rosterRequestDigest(action, command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(namespace, commandID, digest)
}

func pairingReceipt(command PairingCommand) (idempotency.Command, error) {
	return executionReceipt(adminPairingReceiptNamespace, command.CommandID, command)
}

func waveReceipt(command WaveCommand) (idempotency.Command, error) {
	return executionReceipt(adminWaveReceiptNamespace, command.CommandID, command)
}

func executionReceipt(namespace string, commandID uuid.UUID, command any) (idempotency.Command, error) {
	digest, err := executionRequestDigest(command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(namespace, commandID, digest)
}

func tournamentActionReceipt(command TournamentActionCommand) (idempotency.Command, error) {
	return adminReceipt(
		adminTournamentActionReceiptNamespace,
		command.CommandID,
		tournamentActionReceiptDigest(command),
	)
}

func noShowReceipt(command NoShowCommand) (idempotency.Command, error) {
	return operatorResultReceipt(adminNoShowReceiptNamespace, OperatorResultActionNoShow, command.CommandID, command)
}

func forfeitReceipt(command ForfeitCommand) (idempotency.Command, error) {
	return operatorResultReceipt(adminForfeitReceiptNamespace, OperatorResultActionForfeit, command.CommandID, command)
}

func operatorResultReceipt(
	namespace string,
	action OperatorResultAction,
	commandID uuid.UUID,
	command any,
) (idempotency.Command, error) {
	digest, err := operatorResultDigest(action, command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(namespace, commandID, digest)
}

func reserveReceipt(command ReserveCommand) (idempotency.Command, error) {
	return replayWorkflowReceipt(adminReserveReceiptNamespace, "operator_reserve", command.CommandID, command)
}

func replayReceipt(command ReplayCommand) (idempotency.Command, error) {
	return replayWorkflowReceipt(adminReplayReceiptNamespace, "replacement", command.CommandID, command)
}

func replayWorkflowReceipt(
	namespace string,
	action string,
	commandID uuid.UUID,
	command any,
) (idempotency.Command, error) {
	digest, err := replayWorkflowDigest(action, command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(namespace, commandID, digest)
}

func correctionReceipt(command CorrectionCommand) (idempotency.Command, error) {
	digest, err := correctionRequestDigest(command)
	if err != nil {
		return idempotency.Command{}, err
	}
	return adminReceipt(adminCorrectionReceiptNamespace, command.CommandID, digest)
}

func adminReceipt(namespace string, commandID uuid.UUID, digest [sha256.Size]byte) (idempotency.Command, error) {
	return idempotency.NewCommand(namespace, commandID, digest)
}

func tournamentActionReceiptDigest(command TournamentActionCommand) [sha256.Size]byte {
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

func writeReceiptUUID(payload *bytes.Buffer, value uuid.UUID) {
	_, _ = payload.Write(value[:])
}

func writeReceiptInt64(payload *bytes.Buffer, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = payload.Write(encoded[:])
}

func writeReceiptString(payload *bytes.Buffer, value string) {
	writeReceiptInt64(payload, int64(len(value)))
	_, _ = payload.WriteString(value)
}
