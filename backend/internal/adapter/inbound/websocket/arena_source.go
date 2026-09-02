package websocket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arenausecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

var (
	ErrArenaSnapshotSource           = errors.New("arena snapshot source invalid")
	ErrArenaSnapshotParticipantScope = errors.New("arena snapshot participant scope denied")
)

const (
	arenaSnapshotRoleParticipant = "participant"
	arenaSnapshotRolePublic      = "public"
	arenaSnapshotRoleOperator    = "operator"
)

type ArenaTournamentSnapshotReader interface {
	GetTournament(ctx context.Context, id uuid.UUID) (*arenausecase.TournamentRecord, error)
}

type ArenaRosterSnapshotReader interface {
	ListRosterParticipants(ctx context.Context, rosterID uuid.UUID) ([]arenausecase.ParticipantRecord, error)
}

type ArenaProductionSnapshotSource struct {
	tournaments ArenaTournamentSnapshotReader
	rosters     ArenaRosterSnapshotReader
}

var (
	_ arenaws.ParticipantRealtimeReadSource = (*ArenaProductionSnapshotSource)(nil)
	_ arenaws.PublicRealtimeReadSource      = (*ArenaProductionSnapshotSource)(nil)
	_ arenaws.OperatorRealtimeReadSource    = (*ArenaProductionSnapshotSource)(nil)
)

func NewArenaProductionSnapshotSource(
	tournaments ArenaTournamentSnapshotReader,
	rosters ArenaRosterSnapshotReader,
) *ArenaProductionSnapshotSource {
	return &ArenaProductionSnapshotSource{tournaments: tournaments, rosters: rosters}
}

func (s *ArenaProductionSnapshotSource) PublicRealtimeRead(
	ctx context.Context,
	tournamentID uuid.UUID,
) (arenaws.PublicRealtimeReadResult, error) {
	tournament, err := s.loadTournament(ctx, tournamentID)
	if err != nil {
		return arenaws.PublicRealtimeReadResult{}, err
	}

	snapshot, err := arenaws.NewPublicSnapshot(tournament.ID, arenaws.PublicSnapshotInput{
		Revision:     tournament.Revision,
		LastSequence: tournament.Revision,
		Tournament: arenaws.PublicTournamentInput{
			TournamentID: tournament.ID,
			Preset:       tournament.Preset.String(),
			State:        tournament.State.String(),
			RosterSize:   tournament.RosterSize,
			StartedAt:    tournament.StartedAt,
			FinishedAt:   tournament.FinishedAt,
		},
		Scoreboard:      []arenaws.PublicScoreboardEntryInput{},
		Bracket:         []arenaws.PublicBracketMatchInput{},
		LiveSeries:      []arenaws.PublicSeriesInput{},
		OfficialResults: []arenaws.PublicOfficialResultInput{},
	})
	if err != nil {
		return arenaws.PublicRealtimeReadResult{}, fmt.Errorf("%w: public snapshot: %w", ErrArenaSnapshotSource, err)
	}
	metadata := arenaSnapshotMetadata(arenaSnapshotRolePublic, tournament, uuid.Nil)
	if _, err := arenaws.NewRealtimeEnvelope(metadata, snapshot); err != nil {
		return arenaws.PublicRealtimeReadResult{}, fmt.Errorf("%w: public envelope: %w", ErrArenaSnapshotSource, err)
	}

	return arenaws.PublicRealtimeReadResult{
		Snapshot:         snapshot,
		SnapshotMetadata: metadata,
		Available:        arenaSnapshotAvailability(tournament.Revision),
		Events:           []arenaws.RealtimeEnvelope{},
	}, nil
}

func (s *ArenaProductionSnapshotSource) ReadParticipantRealtime(
	ctx context.Context,
	query arenaws.ParticipantRealtimeReadQuery,
) (arenaws.ParticipantRealtimeReadModel, error) {
	if query.PlayerID == uuid.Nil || query.MaxEvents < 1 {
		return arenaws.ParticipantRealtimeReadModel{}, ErrArenaSnapshotParticipantScope
	}
	tournament, err := s.loadTournament(ctx, query.TournamentID)
	if err != nil {
		return arenaws.ParticipantRealtimeReadModel{}, err
	}
	if s.rosters == nil {
		return arenaws.ParticipantRealtimeReadModel{}, ErrArenaSnapshotSource
	}
	participants, err := s.rosters.ListRosterParticipants(ctx, tournament.RosterID)
	if err != nil {
		return arenaws.ParticipantRealtimeReadModel{}, fmt.Errorf("%w: list roster: %w", ErrArenaSnapshotSource, err)
	}
	member, err := arenaRosterContainsPlayer(tournament, participants, query.PlayerID)
	if err != nil {
		return arenaws.ParticipantRealtimeReadModel{}, err
	}
	if !member {
		return arenaws.ParticipantRealtimeReadModel{}, ErrArenaSnapshotParticipantScope
	}

	snapshot, err := arenaws.NewParticipantSnapshot(
		arenaws.ParticipantSnapshotScope{TournamentID: tournament.ID, PlayerID: query.PlayerID},
		arenaws.ParticipantSnapshotInput{
			TournamentID: tournament.ID,
			PlayerID:     query.PlayerID,
			Revision:     tournament.Revision,
			LastSequence: tournament.Revision,
		},
	)
	if err != nil {
		return arenaws.ParticipantRealtimeReadModel{}, fmt.Errorf("%w: participant snapshot: %w", ErrArenaSnapshotSource, err)
	}
	metadata := arenaSnapshotMetadata(arenaSnapshotRoleParticipant, tournament, query.PlayerID)
	if _, err := arenaws.NewRealtimeEnvelope(metadata, snapshot); err != nil {
		return arenaws.ParticipantRealtimeReadModel{}, fmt.Errorf("%w: participant envelope: %w", ErrArenaSnapshotSource, err)
	}

	return arenaws.ParticipantRealtimeReadModel{
		Snapshot:  arenaws.ParticipantRealtimeSnapshot{Metadata: metadata, Payload: snapshot},
		Available: arenaSnapshotAvailability(tournament.Revision),
		Events:    []arenaws.RealtimeEnvelope{},
	}, nil
}

func (s *ArenaProductionSnapshotSource) ReadOperatorRealtime(
	ctx context.Context,
	query arenaws.OperatorRealtimeQuery,
) (arenaws.OperatorRealtimeState, error) {
	if query.ReplayLimit < 1 || !arenaOperatorCursorValid(query.Cursor, query.TournamentID) {
		return arenaws.OperatorRealtimeState{}, ErrArenaSnapshotSource
	}
	tournament, err := s.loadTournament(ctx, query.TournamentID)
	if err != nil {
		return arenaws.OperatorRealtimeState{}, err
	}
	metadata := arenaSnapshotMetadata(arenaSnapshotRoleOperator, tournament, uuid.Nil)
	snapshot, err := arenaws.NewOperatorSnapshot(
		arenaws.OperatorSnapshotAccess{
			Authenticated: true,
			TournamentID:  tournament.ID,
			OperatorID:    metadata.EventID,
		},
		arenaws.OperatorSnapshotInput{
			TournamentID:       tournament.ID,
			Revision:           tournament.Revision,
			LastSequence:       tournament.Revision,
			CorrectionRevision: tournament.Revision,
			Waves:              []arenaws.OperatorWaveInput{},
			Presence:           []arenaws.OperatorPresenceInput{},
			Replays:            []arenaws.OperatorReplayInput{},
			AuditLinks:         []arenaws.OperatorAuditLinkInput{},
		},
	)
	if err != nil {
		return arenaws.OperatorRealtimeState{}, fmt.Errorf("%w: operator snapshot: %w", ErrArenaSnapshotSource, err)
	}
	envelope, err := arenaws.NewRealtimeEnvelope(metadata, snapshot)
	if err != nil {
		return arenaws.OperatorRealtimeState{}, fmt.Errorf("%w: operator envelope: %w", ErrArenaSnapshotSource, err)
	}

	return arenaws.OperatorRealtimeState{
		Available: arenaSnapshotAvailability(tournament.Revision),
		Events:    []arenaws.RealtimeEnvelope{},
		Snapshot:  envelope,
	}, nil
}

func (s *ArenaProductionSnapshotSource) loadTournament(
	ctx context.Context,
	tournamentID uuid.UUID,
) (arenausecase.TournamentRecord, error) {
	if ctx == nil || tournamentID == uuid.Nil || s == nil || s.tournaments == nil {
		return arenausecase.TournamentRecord{}, ErrArenaSnapshotSource
	}
	record, err := s.tournaments.GetTournament(ctx, tournamentID)
	if err != nil {
		return arenausecase.TournamentRecord{}, fmt.Errorf("%w: get tournament: %w", ErrArenaSnapshotSource, err)
	}
	if record == nil {
		return arenausecase.TournamentRecord{}, ErrArenaSnapshotSource
	}

	tournament := *record
	if tournament.PausedFromState != nil {
		pausedFrom := *tournament.PausedFromState
		tournament.PausedFromState = &pausedFrom
	}
	tournament.CreatedAt = arenaSnapshotUTC(tournament.CreatedAt)
	tournament.UpdatedAt = arenaSnapshotUTC(tournament.UpdatedAt)
	tournament.StartedAt = arenaSnapshotUTCPointer(tournament.StartedAt)
	tournament.FinishedAt = arenaSnapshotUTCPointer(tournament.FinishedAt)
	if !validArenaSnapshotTournament(tournament, tournamentID) {
		return arenausecase.TournamentRecord{}, ErrArenaSnapshotSource
	}
	return tournament, nil
}

func validArenaSnapshotTournament(tournament arenausecase.TournamentRecord, requestedID uuid.UUID) bool {
	if tournament.ID != requestedID || tournament.RosterID == uuid.Nil || !tournament.Preset.IsValid() ||
		tournament.Revision < 1 || tournament.RosterSize < 0 || tournament.RosterSize > domain.ArenaMaxParticipants {
		return false
	}
	if err := (domain.ArenaTournament{State: tournament.State, PausedFromState: tournament.PausedFromState}).Validate(); err != nil {
		return false
	}
	return validArenaSnapshotTimes(tournament)
}

func validArenaSnapshotTimes(tournament arenausecase.TournamentRecord) bool {
	if tournament.CreatedAt.IsZero() || tournament.UpdatedAt.IsZero() || tournament.UpdatedAt.Before(tournament.CreatedAt) {
		return false
	}
	for _, timestamp := range []*time.Time{tournament.StartedAt, tournament.FinishedAt} {
		if timestamp != nil && timestamp.IsZero() {
			return false
		}
	}
	return tournament.StartedAt == nil || tournament.FinishedAt == nil || !tournament.FinishedAt.Before(*tournament.StartedAt)
}

func arenaRosterContainsPlayer(
	tournament arenausecase.TournamentRecord,
	participants []arenausecase.ParticipantRecord,
	playerID uuid.UUID,
) (bool, error) {
	if len(participants) != tournament.RosterSize {
		return false, ErrArenaSnapshotSource
	}
	roster := domain.ArenaRoster{
		TournamentID: tournament.ID,
		Participants: make([]domain.ArenaParticipant, len(participants)),
	}
	found := false
	for index, participant := range participants {
		if participant.RosterID != tournament.RosterID || participant.TournamentID != tournament.ID ||
			participant.CreatedAt.IsZero() || participant.UpdatedAt.IsZero() || participant.UpdatedAt.Before(participant.CreatedAt) {
			return false, ErrArenaSnapshotSource
		}
		roster.Participants[index] = domain.ArenaParticipant{
			ID:           participant.ID,
			TournamentID: participant.TournamentID,
			PlayerID:     participant.PlayerID,
			Seed:         participant.Seed,
			Attendance:   participant.Attendance,
		}
		found = found || participant.PlayerID == playerID
	}
	if err := roster.Validate(); err != nil {
		return false, fmt.Errorf("%w: roster: %w", ErrArenaSnapshotSource, err)
	}
	return found, nil
}

func arenaSnapshotMetadata(
	role string,
	tournament arenausecase.TournamentRecord,
	playerID uuid.UUID,
) arenaws.RealtimeEnvelopeMetadata {
	identity := fmt.Sprintf("arena-snapshot:%s:%s:%s:%d", role, tournament.ID, playerID, tournament.Revision)
	return arenaws.RealtimeEnvelopeMetadata{
		SchemaVersion:      arenaws.ArenaRealtimeSchemaVersion,
		TournamentID:       tournament.ID,
		Sequence:           tournament.Revision,
		EventID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)),
		OccurredAt:         tournament.UpdatedAt,
		ProjectionRevision: tournament.Revision,
	}
}

func arenaSnapshotAvailability(revision int64) arenaws.RealtimeAvailableRange {
	return arenaws.RealtimeAvailableRange{CurrentProjectionRevision: revision}
}

func arenaOperatorCursorValid(cursor *arenaws.RealtimeCursor, tournamentID uuid.UUID) bool {
	return cursor == nil ||
		(cursor.SchemaVersion == arenaws.ArenaRealtimeSchemaVersion &&
			cursor.TournamentID == tournamentID &&
			cursor.LastSequence >= 1 &&
			cursor.ProjectionRevision >= 1)
}

func arenaSnapshotUTC(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.Round(0).UTC()
}

func arenaSnapshotUTCPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := arenaSnapshotUTC(*value)
	return &normalized
}
