package submission_test

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"
)

func TestGoldenSubmissionScopeIsValid(t *testing.T) {
	t.Parallel()

	valid := goldenSubmissionScopeFixture()
	require.True(t, valid.IsValid())

	tests := []struct {
		name   string
		mutate func(*goldenusecase.GoldenSubmissionScope)
	}{
		{name: "zero tournament", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.State.TournamentID = uuid.Nil }},
		{name: "zero group", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.State.GroupID = uuid.Nil }},
		{name: "zero group revision", mutate: func(scope *goldenusecase.GoldenSubmissionScope) {
			scope.State.GroupRevisionID = domain.DerivedRevisionID{}
		}},
		{name: "zero attempt", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.AttemptID = uuid.Nil }},
		{name: "zero wave", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.WaveID = uuid.Nil }},
		{name: "zero assignment", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.AssignmentID = uuid.Nil }},
		{name: "zero snapshot", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.SnapshotID = uuid.Nil }},
		{name: "zero task", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.TaskID = uuid.Nil }},
		{name: "duplicate group", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.State.GroupID = scope.State.TournamentID }},
		{name: "duplicate group revision", mutate: func(scope *goldenusecase.GoldenSubmissionScope) {
			scope.State.GroupRevisionID = domain.DerivedRevisionID(scope.State.TournamentID)
		}},
		{name: "duplicate attempt", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.AttemptID = scope.State.TournamentID }},
		{name: "duplicate wave", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.WaveID = scope.State.TournamentID }},
		{name: "duplicate assignment", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.AssignmentID = scope.State.TournamentID }},
		{name: "duplicate snapshot", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.SnapshotID = scope.State.TournamentID }},
		{name: "duplicate task", mutate: func(scope *goldenusecase.GoldenSubmissionScope) { scope.TaskID = scope.State.TournamentID }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			candidate := valid
			testCase.mutate(&candidate)
			require.False(t, candidate.IsValid())
		})
	}
}

func TestGoldenSubmissionLedgerExpectationEqual(t *testing.T) {
	t.Parallel()

	expectation := goldenusecase.GoldenSubmissionLedgerExpectation{
		Scope:            goldenSubmissionScopeFixture(),
		RevisionID:       goldenSubmissionID(9),
		Revision:         3,
		NextSubmissionID: 5,
		PayloadDigest:    sha256.Sum256([]byte("golden submission ledger")),
	}
	same := expectation
	require.True(t, expectation.Equal(same))

	tests := []struct {
		name   string
		mutate func(*goldenusecase.GoldenSubmissionLedgerExpectation)
	}{
		{name: "scope", mutate: func(value *goldenusecase.GoldenSubmissionLedgerExpectation) {
			value.Scope.TaskID = goldenSubmissionID(10)
		}},
		{name: "revision identifier", mutate: func(value *goldenusecase.GoldenSubmissionLedgerExpectation) {
			value.RevisionID = goldenSubmissionID(10)
		}},
		{name: "revision", mutate: func(value *goldenusecase.GoldenSubmissionLedgerExpectation) { value.Revision++ }},
		{name: "next submission identifier", mutate: func(value *goldenusecase.GoldenSubmissionLedgerExpectation) { value.NextSubmissionID++ }},
		{name: "payload digest", mutate: func(value *goldenusecase.GoldenSubmissionLedgerExpectation) {
			value.PayloadDigest = sha256.Sum256([]byte("different ledger"))
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			other := expectation
			testCase.mutate(&other)
			require.False(t, expectation.Equal(other))
		})
	}
}

func goldenSubmissionScopeFixture() goldenusecase.GoldenSubmissionScope {
	return goldenusecase.GoldenSubmissionScope{
		State: goldenusecase.GoldenStateScope{
			TournamentID:    goldenSubmissionID(1),
			GroupID:         goldenSubmissionID(2),
			GroupRevisionID: domain.DerivedRevisionID(goldenSubmissionID(3)),
		},
		AttemptID:    goldenSubmissionID(4),
		WaveID:       goldenSubmissionID(5),
		AssignmentID: goldenSubmissionID(6),
		SnapshotID:   goldenSubmissionID(7),
		TaskID:       goldenSubmissionID(8),
	}
}

func goldenSubmissionID(value byte) uuid.UUID {
	var id uuid.UUID
	id[len(id)-1] = value
	return id
}
