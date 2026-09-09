package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
)

func TestTournamentCancellationEvidenceIDsAreStableAndDistinct(t *testing.T) {
	t.Parallel()

	commandID := uuid.MustParse("74000000-0000-0000-0000-000000000001")
	first := newCancellationEvidence(commandID)
	second := newCancellationEvidence(commandID)
	require.Equal(t, first, second)
	require.NotEqual(t, uuid.Nil, first.auditID)
	require.NotEqual(t, uuid.Nil, first.outboxID)
	require.NotEqual(t, uuid.Nil, first.idempotencyKey)
	require.NotEqual(t, first.auditID, first.outboxID)
	require.NotEqual(t, first.auditID, first.idempotencyKey)
	require.NotEqual(t, first.outboxID, first.idempotencyKey)
	require.NotEqual(t, first, newCancellationEvidence(uuid.New()))
}

func TestValidTournamentCancellationInput(t *testing.T) {
	t.Parallel()

	valid := cancellationInputFixture()
	require.True(t, validCancellationInput(valid))

	tests := map[string]func(*tournamentcancellation.TournamentCancellationInput){
		"nil tournament": func(in *tournamentcancellation.TournamentCancellationInput) {
			in.TournamentID = uuid.Nil
		},
		"terminal state": func(in *tournamentcancellation.TournamentCancellationInput) {
			in.ExpectedState = domain.TournamentStateCompleted
		},
		"untrimmed reason": func(in *tournamentcancellation.TournamentCancellationInput) {
			in.Reason = " operator request "
		},
		"oversized reason": func(in *tournamentcancellation.TournamentCancellationInput) {
			in.Reason = strings.Repeat("x", maxCancellationReasonLength+1)
		},
		"non UTC time": func(in *tournamentcancellation.TournamentCancellationInput) {
			in.CancelledAt = in.CancelledAt.In(time.FixedZone("source", 3*60*60))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := valid
			mutate(&input)
			require.False(t, validCancellationInput(input))
		})
	}
}

func TestFoundTournamentCancellationMapsDurableEvidence(t *testing.T) {
	t.Parallel()

	row := cancellationRowFixture()
	record, err := foundCancellationRecord(row)
	require.NoError(t, err)
	require.Equal(t, row.TournamentID, record.Tournament.ID)
	require.Equal(t, domain.TournamentStateCancelled, record.Tournament.State)
	require.Equal(t, row.ResultingRevision, record.Tournament.Revision)
	require.Equal(t, row.CommandID, record.CommandID)
	require.Equal(t, row.ActorID, record.ActorID)
	require.Equal(t, row.Reason, record.Reason)
	require.Equal(t, row.CancelledAt.Time.UTC(), record.CancelledAt)
	require.Nil(t, record.ChampionID)

	t.Run("rejects aliased audit and outbox evidence", func(t *testing.T) {
		t.Parallel()

		invalid := row
		invalid.OutboxEventID = invalid.AuditEventID
		_, err := foundCancellationRecord(invalid)
		require.ErrorIs(t, err, domain.ErrInternal)
	})

	t.Run("rejects stale tournament revision", func(t *testing.T) {
		t.Parallel()

		invalid := row
		invalid.TournamentRevision++
		_, err := foundCancellationRecord(invalid)
		require.ErrorIs(t, err, domain.ErrInternal)
	})

	t.Run("rejects cancellation time drift", func(t *testing.T) {
		t.Parallel()

		invalid := row
		invalid.TournamentFinishedAt = timestamp(row.CancelledAt.Time.Add(time.Second))
		_, err := foundCancellationRecord(invalid)
		require.ErrorIs(t, err, domain.ErrInternal)
	})

	t.Run("rejects terminal source state", func(t *testing.T) {
		t.Parallel()

		invalid := row
		invalid.SourceState = string(domain.TournamentStateCompleted)
		_, err := foundCancellationRecord(invalid)
		require.ErrorIs(t, err, domain.ErrInternal)
	})
}

func TestCreatedTournamentCancellationMapsGeneratedCreateRow(t *testing.T) {
	t.Parallel()

	row := cancellationRowFixture()
	created, err := createdCancellationRecordFromCreate(sqlc.CreateTournamentCancellationRow{
		CommandID:         row.CommandID,
		TournamentID:      row.TournamentID,
		RosterID:          row.RosterID,
		SourceRevision:    row.SourceRevision,
		ResultingRevision: row.ResultingRevision,
		SourceState:       row.SourceState,
		ActorID:           row.ActorID,
		Reason:            row.Reason,
		AuditEventID:      row.AuditEventID,
		OutboxEventID:     row.OutboxEventID,
		CancelledAt:       row.CancelledAt,
		CreatedAt:         row.CreatedAt,
	}, sqlc.Tournament{
		ID: row.TournamentID, State: row.TournamentState, PausedFromState: row.PausedFromState,
		Revision: row.TournamentRevision, UpdatedAt: row.TournamentUpdatedAt, FinishedAt: row.TournamentFinishedAt,
	})
	require.NoError(t, err)
	require.Equal(t, row.CommandID, created.CommandID)
	require.Equal(t, row.TournamentID, created.Tournament.ID)
	require.Equal(t, row.ResultingRevision, created.Tournament.Revision)
}

func TestCancellationAuthorityRequiresExactLiveTournament(t *testing.T) {
	t.Parallel()

	input := cancellationInputFixture()
	pauseOrigin := string(domain.TournamentStateSwiss)
	input.ExpectedState = domain.TournamentStateTechnicalPause
	authority := sqlc.LockTournamentCancellationAuthorityRow{
		ID: input.TournamentID, State: string(input.ExpectedState), PausedFromState: &pauseOrigin,
		Revision: input.ExpectedRevision, UpdatedAt: timestamp(input.CancelledAt.Add(-time.Minute)),
		RosterID: uuid.New(), ProjectionRevisionID: uuid.New(), ProjectionRevision: 3,
	}
	require.True(t, cancellationAuthorityMatches(authority, input))

	authority.Revision++
	require.False(t, cancellationAuthorityMatches(authority, input))
	authority.Revision = input.ExpectedRevision
	authority.PausedFromState = nil
	require.False(t, cancellationAuthorityMatches(authority, input))
	authority.PausedFromState = &pauseOrigin
	authority.FinishedAt = timestamp(input.CancelledAt)
	require.False(t, cancellationAuthorityMatches(authority, input))
	authority.FinishedAt = pgtype.Timestamptz{}
	authority.ProjectionRevisionID = uuid.Nil
	require.False(t, cancellationAuthorityMatches(authority, input))
}

func TestTournamentCancellationRepositoryRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	var repository *TournamentCancellationPostgres
	_, err := repository.GetTournament(context.Background(), uuid.New())
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = NewTournamentCancellationPostgres(nil).GetTournamentCancellation(
		context.Background(),
		uuid.New(),
		uuid.New(),
	)
	require.ErrorIs(t, err, domain.ErrValidation)
	_, changed, err := repository.CancelTournament(context.Background(), cancellationInputFixture())
	require.ErrorIs(t, err, domain.ErrValidation)
	require.False(t, changed)
}

func TestCancellationTournamentRecordRejectsImpossibleTerminalShape(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.September, 6, 14, 0, 0, 0, time.UTC)
	_, err := cancellationTournamentRecord(sqlc.Tournament{
		ID: uuid.New(), State: string(domain.TournamentStateCancelled), Revision: 2,
		UpdatedAt: timestamp(at),
	})
	require.ErrorIs(t, err, domain.ErrInternal)

	_, err = cancellationTournamentRecord(sqlc.Tournament{
		ID: uuid.New(), State: string(domain.TournamentStateSwiss), Revision: 2,
		UpdatedAt: timestamp(at), FinishedAt: timestamp(at),
	})
	require.ErrorIs(t, err, domain.ErrInternal)
}

func cancellationInputFixture() tournamentcancellation.TournamentCancellationInput {
	return tournamentcancellation.TournamentCancellationInput{
		TournamentID:     uuid.MustParse("74000000-0000-0000-0000-000000000010"),
		ExpectedRevision: 4,
		ExpectedState:    domain.TournamentStateSwiss,
		CommandID:        uuid.MustParse("74000000-0000-0000-0000-000000000011"),
		ActorID:          uuid.MustParse("74000000-0000-0000-0000-000000000012"),
		Reason:           "operator request",
		CancelledAt:      time.Date(2026, time.September, 6, 14, 0, 0, 0, time.UTC),
	}
}

func cancellationRowFixture() sqlc.FindTournamentCancellationRow {
	input := cancellationInputFixture()
	return sqlc.FindTournamentCancellationRow{
		CommandID: input.CommandID, TournamentID: input.TournamentID, RosterID: uuid.New(),
		SourceRevision: input.ExpectedRevision, ResultingRevision: input.ExpectedRevision + 1,
		SourceState: string(input.ExpectedState), ActorID: input.ActorID, Reason: input.Reason,
		AuditEventID: uuid.New(), OutboxEventID: uuid.New(), CancelledAt: timestamp(input.CancelledAt),
		CreatedAt: timestamp(input.CancelledAt), TournamentState: string(domain.TournamentStateCancelled),
		TournamentRevision: input.ExpectedRevision + 1, TournamentUpdatedAt: timestamp(input.CancelledAt),
		TournamentFinishedAt: timestamp(input.CancelledAt),
	}
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
