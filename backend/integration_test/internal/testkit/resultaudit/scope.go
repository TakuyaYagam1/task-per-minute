//go:build integration

// Package resultaudit provides low-level PostgreSQL fixtures for result audit
// integration tests. It deliberately accepts an already prepared game scope;
// draft and assignment fixture construction stays with their own test lanes.
package resultaudit

import (
	"time"

	"github.com/google/uuid"
)

// Scope identifies the immutable graph that a result audit fixture extends.
// Result audit migration fixtures always use the two participants already
// bound to the prepared series.
type Scope struct {
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	SeriesID       uuid.UUID
	AttemptID      uuid.UUID
	AssignmentID   uuid.UUID
	ParticipantIDs [2]uuid.UUID
}

// Fixture is the locked result scope returned by LockSeries.
type Fixture struct {
	Scope                  Scope
	InitialScoreRevisionID uuid.UUID
	LockedAt               time.Time
}

// SubmissionInput describes the accepted submission event to append to a
// locked fixture. Database constraints remain the source of truth for digest
// lengths, participant identity, and sequence validity.
type SubmissionInput struct {
	ID             uuid.UUID
	IdempotencyKey uuid.UUID
	ParticipantID  uuid.UUID
	PayloadDigest  []byte
	IntentDigest   []byte
	ReceivedAt     time.Time
}

// Submission is the durable identity returned after an accepted submission
// event is appended.
type Submission struct {
	ID             uuid.UUID
	IdempotencyKey uuid.UUID
	ServerSequence int64
}

// CommitIDs holds the immutable IDs used by one solved result commit.
type CommitIDs struct {
	ResultEventID             uuid.UUID
	ResultEventIdempotencyKey uuid.UUID
	GameResultRevisionID      uuid.UUID
	SeriesScoreRevisionID     uuid.UUID
	SeriesResultRevisionID    uuid.UUID
	AuditEventID              uuid.UUID
	OutboxEventID             uuid.UUID
	ProjectionEvidenceID      uuid.UUID
	OutboxIdempotencyKey      uuid.UUID
	CommitIdempotencyKey      uuid.UUID
}

// CommitInput contains the result commit identity and its expected-head
// transition. AdvanceCurrentHeads=false intentionally exercises the database
// current-head guard and returns its commit-time error.
type CommitInput struct {
	SubmissionID        uuid.UUID
	IDs                 CommitIDs
	WinnerID            uuid.UUID
	SettledAt           time.Time
	AdvanceCurrentHeads bool
}

// CommitResult exposes the durable IDs and projection source selected for the
// solved result commit.
type CommitResult struct {
	IDs                      CommitIDs
	SourceProjectionID       uuid.UUID
	SourceProjectionRevision int64
	ProjectionOrdinal        int16
	SettledAt                time.Time
}
