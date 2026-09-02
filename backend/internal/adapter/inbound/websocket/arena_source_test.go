package websocket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arenausecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

var (
	_ ArenaTournamentSnapshotReader = (arenausecase.TournamentRepository)(nil)
	_ ArenaRosterSnapshotReader     = (arenausecase.AttendanceRepository)(nil)

	_ arenaws.ParticipantRealtimeReadSource = (*ArenaProductionSnapshotSource)(nil)
	_ arenaws.PublicRealtimeReadSource      = (*ArenaProductionSnapshotSource)(nil)
	_ arenaws.OperatorRealtimeReadSource    = (*ArenaProductionSnapshotSource)(nil)
)

func TestArenaProductionSnapshotSource(t *testing.T) {
	t.Run("public snapshot exposes only authoritative summary", func(t *testing.T) {
		repository, tournament, _ := newArenaSourceFixture()
		source := NewArenaProductionSnapshotSource(repository, repository)

		read, err := source.PublicRealtimeRead(context.Background(), tournament.ID)
		require.NoError(t, err)
		require.NoError(t, read.Snapshot.Validate())
		assert.Equal(t, tournament.ID, read.Snapshot.Tournament.TournamentID)
		assert.Equal(t, tournament.Preset.String(), read.Snapshot.Tournament.Preset)
		assert.Equal(t, tournament.State.String(), read.Snapshot.Tournament.State)
		assert.Equal(t, tournament.RosterSize, read.Snapshot.Tournament.RosterSize)
		require.NotNil(t, read.Snapshot.Tournament.StartedAt)
		require.NotNil(t, read.Snapshot.Tournament.FinishedAt)
		assert.Equal(t, tournament.StartedAt.UTC(), *read.Snapshot.Tournament.StartedAt)
		assert.Equal(t, tournament.FinishedAt.UTC(), *read.Snapshot.Tournament.FinishedAt)
		assert.Same(t, time.UTC, read.Snapshot.Tournament.StartedAt.Location())
		assert.Same(t, time.UTC, read.Snapshot.Tournament.FinishedAt.Location())
		assert.NotNil(t, read.Snapshot.Scoreboard)
		assert.NotNil(t, read.Snapshot.Bracket)
		assert.NotNil(t, read.Snapshot.LiveSeries)
		assert.NotNil(t, read.Snapshot.OfficialResults)
		assert.Empty(t, read.Snapshot.Scoreboard)
		assert.Empty(t, read.Snapshot.Bracket)
		assert.Empty(t, read.Snapshot.LiveSeries)
		assert.Empty(t, read.Snapshot.OfficialResults)
		assert.Nil(t, read.Snapshot.Draft)
		assert.Equal(t, noArenaReplayRange(tournament.Revision), read.Available)
		assert.NotNil(t, read.Events)
		assert.Empty(t, read.Events)

		originalStart := *read.Snapshot.Tournament.StartedAt
		*tournament.StartedAt = tournament.StartedAt.Add(time.Hour)
		assert.Equal(t, originalStart, *read.Snapshot.Tournament.StartedAt)
	})

	t.Run("participant snapshot requires exact roster membership", func(t *testing.T) {
		repository, tournament, participants := newArenaSourceFixture()
		source := NewArenaProductionSnapshotSource(repository, repository)

		read, err := source.ReadParticipantRealtime(context.Background(), arenaws.ParticipantRealtimeReadQuery{
			TournamentID: tournament.ID,
			PlayerID:     participants[0].PlayerID,
			MaxEvents:    arenaws.ParticipantRealtimeMaxReplayEvents,
		})
		require.NoError(t, err)
		require.NoError(t, read.Snapshot.Payload.Validate())
		assert.Equal(t, tournament.ID, read.Snapshot.Payload.TournamentID)
		assert.Equal(t, participants[0].PlayerID, read.Snapshot.Payload.PlayerID)
		assert.Equal(t, tournament.Revision, read.Snapshot.Payload.Revision)
		assert.Equal(t, tournament.Revision, read.Snapshot.Payload.LastSequence)
		assert.Nil(t, read.Snapshot.Payload.Assignment)
		assert.Nil(t, read.Snapshot.Payload.Opponent)
		assert.Equal(t, noArenaReplayRange(tournament.Revision), read.Available)
		assert.NotNil(t, read.Events)
		assert.Empty(t, read.Events)

		_, err = source.ReadParticipantRealtime(context.Background(), arenaws.ParticipantRealtimeReadQuery{
			TournamentID: tournament.ID,
			PlayerID:     uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
			MaxEvents:    arenaws.ParticipantRealtimeMaxReplayEvents,
		})
		require.ErrorIs(t, err, ErrArenaSnapshotParticipantScope)

		tournament.RosterSize++
		_, err = source.ReadParticipantRealtime(context.Background(), arenaws.ParticipantRealtimeReadQuery{
			TournamentID: tournament.ID,
			PlayerID:     participants[0].PlayerID,
			MaxEvents:    arenaws.ParticipantRealtimeMaxReplayEvents,
		})
		require.ErrorIs(t, err, ErrArenaSnapshotSource)
	})

	t.Run("operator snapshot contains only supported projections", func(t *testing.T) {
		repository, tournament, _ := newArenaSourceFixture()
		source := NewArenaProductionSnapshotSource(repository, repository)

		state, err := source.ReadOperatorRealtime(context.Background(), arenaws.OperatorRealtimeQuery{
			TournamentID: tournament.ID,
			ReplayLimit:  arenaws.OperatorRealtimeReplayLimit,
		})
		require.NoError(t, err)
		require.NoError(t, state.Snapshot.Validate())
		require.NotNil(t, state.Snapshot.Operator)
		assert.Nil(t, state.Snapshot.Participant)
		assert.Nil(t, state.Snapshot.Public)
		assert.Equal(t, tournament.ID, state.Snapshot.Operator.TournamentID)
		assert.Equal(t, tournament.Revision, state.Snapshot.Operator.Revision)
		assert.Equal(t, tournament.Revision, state.Snapshot.Operator.LastSequence)
		assert.Equal(t, tournament.Revision, state.Snapshot.Operator.CorrectionRevision)
		assert.NotNil(t, state.Snapshot.Operator.Waves)
		assert.NotNil(t, state.Snapshot.Operator.Presence)
		assert.NotNil(t, state.Snapshot.Operator.Replays)
		assert.NotNil(t, state.Snapshot.Operator.AuditLinks)
		assert.Empty(t, state.Snapshot.Operator.Waves)
		assert.Empty(t, state.Snapshot.Operator.Presence)
		assert.Empty(t, state.Snapshot.Operator.Replays)
		assert.Empty(t, state.Snapshot.Operator.AuditLinks)
		assert.Nil(t, state.Snapshot.Operator.Pause)
		assert.Equal(t, noArenaReplayRange(tournament.Revision), state.Available)
		assert.NotNil(t, state.Events)
		assert.Empty(t, state.Events)
	})

	t.Run("metadata is stable and scoped to its inputs", func(t *testing.T) {
		repository, tournament, participants := newArenaSourceFixture()
		source := NewArenaProductionSnapshotSource(repository, repository)

		first, err := source.PublicRealtimeRead(context.Background(), tournament.ID)
		require.NoError(t, err)
		second, err := source.PublicRealtimeRead(context.Background(), tournament.ID)
		require.NoError(t, err)
		assert.Equal(t, first.SnapshotMetadata, second.SnapshotMetadata)
		assert.Equal(t, arenaws.ArenaRealtimeSchemaVersion, first.SnapshotMetadata.SchemaVersion)
		assert.Equal(t, tournament.ID, first.SnapshotMetadata.TournamentID)
		assert.Equal(t, tournament.Revision, first.SnapshotMetadata.Sequence)
		assert.Equal(t, tournament.Revision, first.SnapshotMetadata.ProjectionRevision)
		assert.Equal(t, tournament.UpdatedAt.UTC(), first.SnapshotMetadata.OccurredAt)
		assert.Same(t, time.UTC, first.SnapshotMetadata.OccurredAt.Location())
		assert.NotEqual(t, uuid.Nil, first.SnapshotMetadata.EventID)

		participant, err := source.ReadParticipantRealtime(context.Background(), arenaws.ParticipantRealtimeReadQuery{
			TournamentID: tournament.ID,
			PlayerID:     participants[0].PlayerID,
			MaxEvents:    arenaws.ParticipantRealtimeMaxReplayEvents,
		})
		require.NoError(t, err)
		otherParticipant, err := source.ReadParticipantRealtime(context.Background(), arenaws.ParticipantRealtimeReadQuery{
			TournamentID: tournament.ID,
			PlayerID:     participants[1].PlayerID,
			MaxEvents:    arenaws.ParticipantRealtimeMaxReplayEvents,
		})
		require.NoError(t, err)
		operator, err := source.ReadOperatorRealtime(context.Background(), arenaws.OperatorRealtimeQuery{
			TournamentID: tournament.ID,
			ReplayLimit:  arenaws.OperatorRealtimeReplayLimit,
		})
		require.NoError(t, err)
		assert.NotEqual(t, first.SnapshotMetadata.EventID, participant.Snapshot.Metadata.EventID)
		assert.NotEqual(t, participant.Snapshot.Metadata.EventID, otherParticipant.Snapshot.Metadata.EventID)
		assert.NotEqual(t, first.SnapshotMetadata.EventID, operator.Snapshot.EventID)

		previousEventID := first.SnapshotMetadata.EventID
		tournament.Revision++
		revised, err := source.PublicRealtimeRead(context.Background(), tournament.ID)
		require.NoError(t, err)
		assert.NotEqual(t, previousEventID, revised.SnapshotMetadata.EventID)
	})

	t.Run("invalid source data fails closed", func(t *testing.T) {
		t.Run("missing tournament", func(t *testing.T) {
			repository, tournament, _ := newArenaSourceFixture()
			repository.tournament = nil

			_, err := NewArenaProductionSnapshotSource(repository, repository).PublicRealtimeRead(
				context.Background(), tournament.ID,
			)
			require.ErrorIs(t, err, ErrArenaSnapshotSource)
		})

		t.Run("invalid revision", func(t *testing.T) {
			repository, tournament, _ := newArenaSourceFixture()
			tournament.Revision = 0

			_, err := NewArenaProductionSnapshotSource(repository, repository).PublicRealtimeRead(
				context.Background(), tournament.ID,
			)
			require.ErrorIs(t, err, ErrArenaSnapshotSource)
		})

		t.Run("cross tournament roster", func(t *testing.T) {
			repository, tournament, participants := newArenaSourceFixture()
			repository.participants[0].TournamentID = uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")

			_, err := NewArenaProductionSnapshotSource(repository, repository).ReadParticipantRealtime(
				context.Background(),
				arenaws.ParticipantRealtimeReadQuery{
					TournamentID: tournament.ID,
					PlayerID:     participants[1].PlayerID,
					MaxEvents:    arenaws.ParticipantRealtimeMaxReplayEvents,
				},
			)
			require.ErrorIs(t, err, ErrArenaSnapshotSource)
		})

		t.Run("repository error", func(t *testing.T) {
			repository, tournament, _ := newArenaSourceFixture()
			repository.tournamentError = errors.New("read failed")

			_, err := NewArenaProductionSnapshotSource(repository, repository).ReadOperatorRealtime(
				context.Background(),
				arenaws.OperatorRealtimeQuery{
					TournamentID: tournament.ID,
					ReplayLimit:  arenaws.OperatorRealtimeReplayLimit,
				},
			)
			require.ErrorIs(t, err, ErrArenaSnapshotSource)
			require.ErrorIs(t, err, repository.tournamentError)
		})
	})
}

type arenaSourceRepositoryStub struct {
	tournament      *arenausecase.TournamentRecord
	participants    []arenausecase.ParticipantRecord
	tournamentError error
	rosterError     error
}

func (r *arenaSourceRepositoryStub) GetTournament(
	_ context.Context,
	_ uuid.UUID,
) (*arenausecase.TournamentRecord, error) {
	return r.tournament, r.tournamentError
}

func (r *arenaSourceRepositoryStub) ListRosterParticipants(
	_ context.Context,
	_ uuid.UUID,
) ([]arenausecase.ParticipantRecord, error) {
	return r.participants, r.rosterError
}

func newArenaSourceFixture() (
	*arenaSourceRepositoryStub,
	*arenausecase.TournamentRecord,
	[]arenausecase.ParticipantRecord,
) {
	tournamentID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	rosterID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	location := time.FixedZone("fixture", 3*60*60)
	createdAt := time.Date(2026, time.September, 2, 9, 0, 0, 0, location)
	updatedAt := createdAt.Add(15 * time.Minute)
	startedAt := createdAt.Add(5 * time.Minute)
	finishedAt := createdAt.Add(45 * time.Minute)
	tournament := &arenausecase.TournamentRecord{
		ID:         tournamentID,
		RosterID:   rosterID,
		Preset:     domain.ArenaPresetV1,
		State:      domain.ArenaTournamentStateCompleted,
		Revision:   7,
		RosterSize: 2,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
		StartedAt:  &startedAt,
		FinishedAt: &finishedAt,
	}
	participants := []arenausecase.ParticipantRecord{
		{
			ID:           uuid.MustParse("33333333-3333-3333-3333-333333333333"),
			RosterID:     rosterID,
			TournamentID: tournamentID,
			PlayerID:     uuid.MustParse("44444444-4444-4444-4444-444444444444"),
			Seed:         1,
			Attendance:   domain.ArenaAttendanceStateCheckedIn,
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
		},
		{
			ID:           uuid.MustParse("55555555-5555-5555-5555-555555555555"),
			RosterID:     rosterID,
			TournamentID: tournamentID,
			PlayerID:     uuid.MustParse("66666666-6666-6666-6666-666666666666"),
			Seed:         2,
			Attendance:   domain.ArenaAttendanceStateRegistered,
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
		},
	}
	return &arenaSourceRepositoryStub{tournament: tournament, participants: participants}, tournament, participants
}

func noArenaReplayRange(revision int64) arenaws.RealtimeAvailableRange {
	return arenaws.RealtimeAvailableRange{CurrentProjectionRevision: revision}
}
