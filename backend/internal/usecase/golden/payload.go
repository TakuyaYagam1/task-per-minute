package golden

import (
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
)

func retainedGoldenRecordIdentityIDs(record RetainedGoldenPrestartRecord) []uuid.UUID {
	identities := []uuid.UUID{record.CommandID, record.RevisionID}
	if record.State == RetainedGoldenPrestartPaused {
		identities = append(identities, record.SessionID)
	} else if record.FreshWindow != nil {
		identities = append(identities,
			record.FreshExecutionRevisionID, record.FreshWaveRevisionID.UUID(), record.FreshWindow.ID,
			record.FreshWaveWindowRevisionID.UUID(), record.FreshWindow.RevisionID,
			record.FreshWindow.ReadinessRevisionID, record.FreshWindow.PresenceRevisionID,
		)
	}
	SortIDs(identities)
	return identities
}

func sealRetainedGoldenPrestartRecord(record RetainedGoldenPrestartRecord) (RetainedGoldenPrestartRecord, error) {
	record.PayloadDigest = [sha256.Size]byte{}
	payload, err := goldenPrestartRecordPayload(record)
	if err != nil {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("encode retained pre-start receipt")
	}
	record.PayloadDigest = sha256.Sum256(payload)
	if err := record.Validate(); err != nil {
		return RetainedGoldenPrestartRecord{}, err
	}
	return record.Snapshot(), nil
}

func goldenPrestartAuthorizationPayload(authorization GoldenPrestartOperatorAuthorization) ([]byte, error) {
	authorization.PayloadDigest = [sha256.Size]byte{}
	return Encode(authorization)
}

func goldenPrestartRecordPayload(record RetainedGoldenPrestartRecord) ([]byte, error) {
	clone := record.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	return Encode(clone)
}

func retainedGoldenPauseCommandDigest(command RetainedGoldenPrestartPauseCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func retainedGoldenResumeCommandDigest(command RetainedGoldenPrestartResumeCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func goldenPrestartError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenPrestartPause, message)
}
