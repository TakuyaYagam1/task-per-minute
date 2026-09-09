//go:build integration

package integration_test

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type reconnectMigrationFixture struct {
	draft               draftMigrationFixture
	attemptID           uuid.UUID
	normalPauseID       uuid.UUID
	normalWaveID        uuid.UUID
	rootPauseID         uuid.UUID
	rootPauseRevisionID uuid.UUID
	gamePauseID         uuid.UUID
	gamePauseRevisionID uuid.UUID
	firstIntervalID     uuid.UUID
	secondIntervalID    uuid.UUID
	firstDeadline       time.Time
	secondDeadline      time.Time
	pausedAt            time.Time
}

type reconnectContinuationInput struct {
	id                 uuid.UUID
	continuedFromID    *uuid.UUID
	suspendedByPauseID *uuid.UUID
	owningPauseID      *uuid.UUID
	gameAttemptID      *uuid.UUID
	participantID      uuid.UUID
	presenceEpoch      int64
	intervalNumber     int
	continuationNumber int
	state              string
	openedAt           time.Time
	deadlineAt         time.Time
	closedAt           *time.Time
	revision           int64
	createdAt          time.Time
	updatedAt          time.Time
}

type reconnectIntervalSnapshot struct {
	id                 uuid.UUID
	pauseID            uuid.UUID
	rosterID           uuid.UUID
	seriesID           uuid.UUID
	gameAttemptID      uuid.UUID
	participantID      uuid.UUID
	presenceEpoch      int64
	intervalNumber     int
	continuationNumber int
	continuedFromID    pgtype.UUID
	suspendedByPauseID pgtype.UUID
	state              string
	openedAt           time.Time
	deadlineAt         time.Time
	closedAt           pgtype.Timestamptz
	revision           int64
	createdAt          time.Time
	updatedAt          time.Time
}
