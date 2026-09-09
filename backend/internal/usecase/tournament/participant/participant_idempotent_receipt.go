package participant

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"

	"github.com/google/uuid"

	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

const participantReceiptVersion = "tournament-participant-receipt-v1"

func readyReceipt(command usecase.ReadyCommand) (idempotency.Command, error) {
	return participantReceipt(participantReadyReceiptNamespace, command.CommandID, func(payload *participantReceiptPayload) {
		payload.uuid(command.Actor.PlayerID)
		payload.uuid(command.TournamentID)
		payload.uuid(command.WaveID)
		payload.int64(command.ExpectedProjectionRevision)
		payload.bool(command.Ready)
	})
}

func draftActionReceipt(command usecase.DraftActionCommand) (idempotency.Command, error) {
	return participantReceipt(participantDraftReceiptNamespace, command.CommandID, func(payload *participantReceiptPayload) {
		payload.uuid(command.Actor.PlayerID)
		payload.uuid(command.TournamentID)
		payload.uuid(command.SeriesID)
		payload.int64(command.ExpectedProjectionRevision)
		payload.int64(command.ExpectedDraftRevision)
		payload.int64(int64(command.ExpectedTurn))
		payload.string(string(command.Action))
		payload.string(string(command.Category))
	})
}

func submissionReceipt(command usecase.SubmissionCommand) (idempotency.Command, error) {
	return participantReceipt(participantSubmissionReceiptNamespace, command.CommandID, func(payload *participantReceiptPayload) {
		payload.uuid(command.Actor.PlayerID)
		payload.uuid(command.TournamentID)
		payload.uuid(command.SeriesID)
		payload.uuid(command.GameID)
		payload.int64(command.ExpectedProjectionRevision)
		payload.string(command.SubmittedFlag)
	})
}

func surrenderReceipt(command usecase.SurrenderCommand) (idempotency.Command, error) {
	return participantReceipt(participantSurrenderReceiptNamespace, command.CommandID, func(payload *participantReceiptPayload) {
		payload.uuid(command.Actor.PlayerID)
		payload.uuid(command.TournamentID)
		payload.uuid(command.SeriesID)
		payload.int64(command.ExpectedProjectionRevision)
		payload.bool(command.Confirmed)
		payload.string(command.Reason)
	})
}

func postSeriesReceipt(command usecase.PostSeriesCommand) (idempotency.Command, error) {
	return participantReceipt(participantPostSeriesReceiptNamespace, command.CommandID, func(payload *participantReceiptPayload) {
		payload.uuid(command.Actor.PlayerID)
		payload.uuid(command.TournamentID)
		payload.uuid(command.SeriesID)
		payload.int64(command.ExpectedProjectionRevision)
		payload.string(string(command.Action))
	})
}

func participantReceipt(
	namespace string,
	commandID uuid.UUID,
	appendFields func(*participantReceiptPayload),
) (idempotency.Command, error) {
	var buffer bytes.Buffer
	payload := participantReceiptPayload{buffer: &buffer}
	payload.string(participantReceiptVersion)
	payload.string(namespace)
	appendFields(&payload)
	return idempotency.NewCommand(namespace, commandID, sha256.Sum256(buffer.Bytes()))
}

type participantReceiptPayload struct {
	buffer *bytes.Buffer
}

func (payload *participantReceiptPayload) uuid(value uuid.UUID) {
	_, _ = payload.buffer.Write(value[:])
}

func (payload *participantReceiptPayload) int64(value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = payload.buffer.Write(encoded[:])
}

func (payload *participantReceiptPayload) bool(value bool) {
	if value {
		payload.buffer.WriteByte(1)
		return
	}
	payload.buffer.WriteByte(0)
}

func (payload *participantReceiptPayload) string(value string) {
	payload.int64(int64(len(value)))
	_, _ = payload.buffer.WriteString(value)
}
