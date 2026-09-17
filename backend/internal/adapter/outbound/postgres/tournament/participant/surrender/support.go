package surrender

import "github.com/google/uuid"

const resultActorServer = "server"

func participantCommandID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("participant-command:"+role))
}

func cloneParticipantUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
