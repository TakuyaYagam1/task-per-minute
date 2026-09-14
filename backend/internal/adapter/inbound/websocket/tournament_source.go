package websocket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

var ErrTournamentSnapshotSource = errors.New("tournament snapshot source invalid")

const (
	tournamentSnapshotRoleParticipant = "participant"
	tournamentSnapshotRolePublic      = "public"
	tournamentSnapshotRoleOperator    = "operator"
)

type TournamentProductionSnapshotSource struct {
	snapshots usecase.TournamentSnapshotUseCase
	golden    usecase.GoldenUseCase
}

func NewTournamentProductionSnapshotSource(
	snapshots usecase.TournamentSnapshotUseCase,
	golden ...usecase.GoldenUseCase,
) (*TournamentProductionSnapshotSource, error) {
	if snapshots == nil {
		return nil, fmt.Errorf("%w: snapshot reader is required", ErrTournamentSnapshotSource)
	}
	source := &TournamentProductionSnapshotSource{snapshots: snapshots}
	if len(golden) > 1 {
		return nil, fmt.Errorf("%w: multiple Golden readers", ErrTournamentSnapshotSource)
	}
	if len(golden) == 1 {
		source.golden = golden[0]
	}
	return source, nil
}

func (source *TournamentProductionSnapshotSource) ReadParticipantRealtime(
	ctx context.Context,
	query tournamentws.ParticipantRealtimeReadQuery,
) (tournamentws.ParticipantRealtimeSnapshot, error) {
	if ctx == nil || source == nil || source.snapshots == nil ||
		query.TournamentID == uuid.Nil || query.PlayerID == uuid.Nil {
		return tournamentws.ParticipantRealtimeSnapshot{}, ErrTournamentSnapshotSource
	}
	view, err := source.snapshots.ParticipantSnapshot(ctx, usecase.ParticipantSnapshotQuery{
		TournamentID: query.TournamentID,
		PlayerID:     query.PlayerID,
	})
	if err != nil {
		return tournamentws.ParticipantRealtimeSnapshot{}, fmt.Errorf("%w: participant read: %w", ErrTournamentSnapshotSource, err)
	}
	var golden *usecase.GoldenParticipantView
	if source.golden != nil {
		current, goldenErr := source.golden.ParticipantView(ctx, usecase.GoldenParticipantQuery{
			TournamentID: query.TournamentID, PlayerID: query.PlayerID,
		})
		if goldenErr != nil && !errors.Is(goldenErr, domain.ErrTournamentNotFound) {
			return tournamentws.ParticipantRealtimeSnapshot{}, fmt.Errorf("%w: Golden participant read: %w", ErrTournamentSnapshotSource, goldenErr)
		}
		if goldenErr == nil {
			golden = &current
		}
	}
	snapshot, err := tournamentws.NewParticipantSnapshot(
		tournamentws.ParticipantSnapshotScope(query),
		participantSnapshotInput(view, golden),
	)
	if err != nil {
		return tournamentws.ParticipantRealtimeSnapshot{}, fmt.Errorf("%w: participant snapshot: %w", ErrTournamentSnapshotSource, err)
	}
	metadata, err := tournamentSnapshotMetadata(
		tournamentSnapshotRoleParticipant,
		query.TournamentID,
		query.PlayerID,
		snapshot.Revision,
		snapshot.LastSequence,
		view.Cursor.ObservedAt,
	)
	if err != nil {
		return tournamentws.ParticipantRealtimeSnapshot{}, err
	}
	return tournamentws.ParticipantRealtimeSnapshot{Metadata: metadata, Payload: snapshot}, nil
}

func (source *TournamentProductionSnapshotSource) PublicRealtimeRead(
	ctx context.Context,
	tournamentID uuid.UUID,
) (tournamentws.PublicRealtimeReadResult, error) {
	if ctx == nil || source == nil || source.snapshots == nil || tournamentID == uuid.Nil {
		return tournamentws.PublicRealtimeReadResult{}, ErrTournamentSnapshotSource
	}
	view, err := source.snapshots.PublicSnapshot(ctx, usecase.PublicSnapshotQuery{TournamentID: tournamentID})
	if err != nil {
		return tournamentws.PublicRealtimeReadResult{}, fmt.Errorf("%w: public read: %w", ErrTournamentSnapshotSource, err)
	}
	snapshot, err := tournamentws.NewPublicSnapshot(tournamentID, publicSnapshotInput(view))
	if err != nil {
		return tournamentws.PublicRealtimeReadResult{}, fmt.Errorf("%w: public snapshot: %w", ErrTournamentSnapshotSource, err)
	}
	metadata, err := tournamentSnapshotMetadata(
		tournamentSnapshotRolePublic,
		tournamentID,
		uuid.Nil,
		snapshot.Revision,
		snapshot.LastSequence,
		view.Cursor.ObservedAt,
	)
	if err != nil {
		return tournamentws.PublicRealtimeReadResult{}, err
	}
	return tournamentws.PublicRealtimeReadResult{Snapshot: snapshot, SnapshotMetadata: metadata}, nil
}

func (source *TournamentProductionSnapshotSource) ReadOperatorRealtime(
	ctx context.Context,
	query tournamentws.OperatorRealtimeQuery,
) (tournamentws.RealtimeEnvelope, error) {
	if ctx == nil || source == nil || source.snapshots == nil ||
		query.TournamentID == uuid.Nil || query.OperatorID == uuid.Nil {
		return tournamentws.RealtimeEnvelope{}, ErrTournamentSnapshotSource
	}
	view, err := source.snapshots.OperatorSnapshot(ctx, usecase.OperatorSnapshotQuery{
		TournamentID: query.TournamentID,
		OperatorID:   query.OperatorID,
	})
	if err != nil {
		return tournamentws.RealtimeEnvelope{}, fmt.Errorf("%w: operator read: %w", ErrTournamentSnapshotSource, err)
	}
	var golden []usecase.GoldenOperatorGroupView
	if source.golden != nil {
		current, goldenErr := source.golden.OperatorView(ctx, usecase.GoldenOperatorQuery{
			TournamentID: query.TournamentID, OperatorID: query.OperatorID,
		})
		if goldenErr != nil && !errors.Is(goldenErr, domain.ErrTournamentNotFound) {
			return tournamentws.RealtimeEnvelope{}, fmt.Errorf("%w: Golden operator read: %w", ErrTournamentSnapshotSource, goldenErr)
		}
		if goldenErr == nil {
			golden = current.Groups
		}
	}
	snapshot, err := tournamentws.NewOperatorSnapshot(
		tournamentws.OperatorSnapshotAccess{
			Authenticated: true,
			TournamentID:  query.TournamentID,
			OperatorID:    query.OperatorID,
		},
		operatorSnapshotInput(view, golden),
	)
	if err != nil {
		return tournamentws.RealtimeEnvelope{}, fmt.Errorf("%w: operator snapshot: %w", ErrTournamentSnapshotSource, err)
	}
	metadata, err := tournamentSnapshotMetadata(
		tournamentSnapshotRoleOperator,
		query.TournamentID,
		query.OperatorID,
		snapshot.Revision,
		snapshot.LastSequence,
		view.Cursor.ObservedAt,
	)
	if err != nil {
		return tournamentws.RealtimeEnvelope{}, err
	}
	envelope, err := tournamentws.NewRealtimeEnvelope(metadata, snapshot)
	if err != nil {
		return tournamentws.RealtimeEnvelope{}, fmt.Errorf("%w: operator envelope: %w", ErrTournamentSnapshotSource, err)
	}
	return envelope, nil
}

func participantSnapshotInput(view usecase.ParticipantSnapshotView, golden *usecase.GoldenParticipantView) tournamentws.ParticipantSnapshotInput {
	input := tournamentws.ParticipantSnapshotInput{
		TournamentID: view.TournamentID,
		PlayerID:     view.PlayerID,
		Revision:     view.Cursor.ProjectionRevision,
		LastSequence: view.Cursor.EventSequence,
	}
	if view.Game != nil {
		game := view.Game
		input.Game = &tournamentws.ParticipantGameInput{
			GameID:   game.GameID,
			State:    game.State,
			Revision: game.Revision,
		}
		if game.Pause != nil {
			input.Game.Pause = &tournamentws.ParticipantGamePauseInput{
				PauseID:           game.Pause.PauseID,
				State:             game.Pause.State,
				FrozenAt:          game.Pause.FrozenAt,
				FrozenRemainingMS: game.Pause.FrozenRemainingMS,
				ResumedAt:         game.Pause.ResumedAt,
				ResumedDeadline:   game.Pause.ResumedDeadline,
				ReconnectDeadline: game.Pause.ReconnectDeadline,
			}
		}
	}
	if golden != nil {
		input.Golden = participantGoldenInput(*golden)
	}
	if view.Assignment != nil {
		assignment := view.Assignment
		input.Assignment = &tournamentws.ParticipantAssignmentInput{
			TournamentID: view.TournamentID,
			PlayerID:     view.PlayerID,
			AssignmentID: assignment.AssignmentID,
			AttemptID:    assignment.AttemptID,
			SeriesID:     assignment.SeriesID,
			GameID:       assignment.GameID,
			WaveID:       assignment.WaveID,
			Task: tournamentws.ParticipantTaskInput{
				SnapshotID:       assignment.Task.SnapshotID,
				TaskID:           assignment.Task.TaskID,
				Title:            assignment.Task.Title,
				Category:         assignment.Task.Category,
				Difficulty:       assignment.Task.Difficulty,
				TimeLimitSeconds: assignment.Task.TimeLimitSeconds,
			},
		}
	}
	if view.Opponent != nil {
		opponent := view.Opponent
		input.Opponent = &tournamentws.OpponentCompetitionInput{
			TournamentID: view.TournamentID,
			PlayerID:     opponent.PlayerID,
			DisplayName:  opponent.DisplayName,
			SeriesID:     opponent.SeriesID,
			Ready:        opponent.Ready,
			SeriesState:  opponent.SeriesState,
			Score:        opponent.Score,
		}
	}
	return input
}

func participantGoldenInput(view usecase.GoldenParticipantView) *tournamentws.ParticipantGoldenInput {
	input := &tournamentws.ParticipantGoldenInput{
		GroupID: view.GroupID, GroupRevisionID: view.GroupRevisionID, AttemptID: view.AttemptID,
		RuntimeRevision: view.RuntimeRevision, ReadyWindowID: view.ReadyWindowID,
		State: view.State, Ready: view.Ready, Submitted: view.Submitted, Position: view.Position,
		StartedAt: view.StartedAt, Deadline: view.Deadline,
	}
	if view.Task != nil {
		input.Task = &tournamentws.ParticipantGoldenTaskInput{
			AssignmentID: view.Task.AssignmentID, SnapshotID: view.Task.SnapshotID, TaskID: view.Task.TaskID,
			Version: view.Task.Version, Title: view.Task.Title, Description: view.Task.Description,
			Category: view.Task.Category, Difficulty: view.Task.Difficulty,
			TimeLimitSeconds: view.Task.TimeLimitSeconds, TaskURL: view.Task.TaskURL,
			SourceFileAvailable: view.Task.SourceFileAvailable,
		}
	}
	return input
}

func publicSnapshotInput(view usecase.PublicSnapshotView) tournamentws.PublicSnapshotInput {
	tournamentID := view.Tournament.TournamentID
	input := tournamentws.PublicSnapshotInput{
		Revision:     view.Cursor.ProjectionRevision,
		LastSequence: view.Cursor.EventSequence,
		Tournament: tournamentws.PublicTournamentInput{
			TournamentID: tournamentID,
			Preset:       view.Tournament.Preset,
			State:        view.Tournament.State,
			RosterSize:   view.Tournament.RosterSize,
			StartedAt:    view.Tournament.StartedAt,
			FinishedAt:   view.Tournament.FinishedAt,
		},
		Scoreboard:      make([]tournamentws.PublicScoreboardEntryInput, len(view.Scoreboard)),
		Bracket:         make([]tournamentws.PublicBracketMatchInput, len(view.Bracket)),
		LiveSeries:      make([]tournamentws.PublicSeriesInput, len(view.LiveSeries)),
		OfficialResults: make([]tournamentws.PublicOfficialResultInput, len(view.OfficialResults)),
	}
	for index, entry := range view.Scoreboard {
		input.Scoreboard[index] = tournamentws.PublicScoreboardEntryInput{
			TournamentID:    tournamentID,
			Rank:            entry.Rank,
			DisplayName:     entry.DisplayName,
			Points:          entry.Points,
			Buchholz:        entry.Buchholz,
			EffectiveTimeMS: entry.EffectiveTimeMS,
		}
	}
	for index, match := range view.Bracket {
		input.Bracket[index] = tournamentws.PublicBracketMatchInput{
			TournamentID:      tournamentID,
			Stage:             match.Stage,
			Position:          match.Position,
			FirstDisplayName:  match.FirstDisplayName,
			SecondDisplayName: match.SecondDisplayName,
			FirstWins:         match.FirstWins,
			SecondWins:        match.SecondWins,
			State:             match.State,
		}
	}
	for index, series := range view.LiveSeries {
		input.LiveSeries[index] = tournamentws.PublicSeriesInput{
			TournamentID:        tournamentID,
			SeriesID:            series.SeriesID,
			Format:              series.Format,
			State:               series.State,
			FirstDisplayName:    series.FirstDisplayName,
			SecondDisplayName:   series.SecondDisplayName,
			FirstWins:           series.FirstWins,
			SecondWins:          series.SecondWins,
			CurrentGamePosition: series.CurrentGamePosition,
		}
	}
	for index, result := range view.OfficialResults {
		input.OfficialResults[index] = tournamentws.PublicOfficialResultInput{
			TournamentID:      tournamentID,
			RevisionID:        result.RevisionID,
			SeriesID:          result.SeriesID,
			State:             result.State,
			WinnerDisplayName: result.WinnerDisplayName,
			FirstWins:         result.FirstWins,
			SecondWins:        result.SecondWins,
			RecordedAt:        result.RecordedAt,
		}
	}
	if view.Draft != nil {
		draft := view.Draft
		input.Draft = &tournamentws.PublicDraftInput{
			TournamentID:       tournamentID,
			SeriesID:           draft.SeriesID,
			Format:             draft.Format,
			State:              draft.State,
			Pool:               append([]string{}, draft.Pool...),
			SelectedCategories: append([]string{}, draft.SelectedCategories...),
			Actions:            make([]tournamentws.PublicDraftActionInput, len(draft.Actions)),
		}
		for index, action := range draft.Actions {
			input.Draft.Actions[index] = tournamentws.PublicDraftActionInput{
				Turn:             action.Turn,
				Action:           action.Action,
				Category:         action.Category,
				ActorDisplayName: action.ActorDisplayName,
				OccurredAt:       action.OccurredAt,
			}
		}
	}
	return input
}

func operatorSnapshotInput(view usecase.OperatorSnapshotView, golden []usecase.GoldenOperatorGroupView) tournamentws.OperatorSnapshotInput {
	input := tournamentws.OperatorSnapshotInput{
		TournamentID: view.TournamentID,
		Revision:     view.Cursor.ProjectionRevision,
		LastSequence: view.Cursor.EventSequence,
		Waves:        make([]tournamentws.OperatorWaveInput, len(view.Waves)),
		Presence:     make([]tournamentws.OperatorPresenceInput, len(view.Presence)),
		Replays:      make([]tournamentws.OperatorReplayInput, len(view.Replays)),
		AuditLinks:   make([]tournamentws.OperatorAuditLinkInput, len(view.AuditLinks)),
		Golden:       make([]tournamentws.OperatorGoldenGroupInput, len(golden)),
	}
	for index, group := range golden {
		members := make([]tournamentws.OperatorGoldenMemberInput, len(group.Members))
		for memberIndex, member := range group.Members {
			members[memberIndex] = tournamentws.OperatorGoldenMemberInput{
				ParticipantID: member.ParticipantID, Ready: member.Ready,
				Submitted: member.Submitted, Position: member.Position,
			}
		}
		input.Golden[index] = tournamentws.OperatorGoldenGroupInput{
			GroupID: group.GroupID, GroupRevisionID: group.GroupRevisionID, AttemptID: group.AttemptID,
			RuntimeRevision: group.RuntimeRevision, ReadyWindowID: group.ReadyWindowID,
			State: group.State, PositionFrom: group.PositionFrom, PositionTo: group.PositionTo,
			StartedAt: group.StartedAt, Deadline: group.Deadline, Members: members,
		}
	}
	for index, wave := range view.Waves {
		input.Waves[index] = tournamentws.OperatorWaveInput{
			TournamentID:   view.TournamentID,
			WaveID:         wave.WaveID,
			State:          wave.State,
			WindowDeadline: wave.WindowDeadline,
			Members:        make([]tournamentws.OperatorWaveMemberInput, len(wave.Members)),
		}
		for memberIndex, member := range wave.Members {
			input.Waves[index].Members[memberIndex] = tournamentws.OperatorWaveMemberInput{
				ParticipantID:     member.ParticipantID,
				SeriesID:          member.SeriesID,
				Ready:             member.Ready,
				ReadinessRevision: member.ReadinessRevision,
			}
		}
	}
	for index, presence := range view.Presence {
		input.Presence[index] = tournamentws.OperatorPresenceInput{
			TournamentID:  view.TournamentID,
			ParticipantID: presence.ParticipantID,
			SeriesID:      presence.SeriesID,
			State:         presence.State,
			PresenceEpoch: presence.PresenceEpoch,
			UpdatedAt:     presence.UpdatedAt,
		}
	}
	for index, replay := range view.Replays {
		input.Replays[index] = tournamentws.OperatorReplayInput{
			TournamentID:      view.TournamentID,
			SeriesID:          replay.SeriesID,
			SlotID:            replay.SlotID,
			FailedGameID:      replay.FailedGameID,
			ReplacementGameID: replay.ReplacementGameID,
			ReplacementWaveID: replay.ReplacementWaveID,
			State:             replay.State,
			Revision:          replay.Revision,
		}
	}
	if view.Pause != nil {
		input.Pause = &tournamentws.OperatorPauseInput{
			TournamentID:      view.TournamentID,
			PauseID:           view.Pause.PauseID,
			State:             view.Pause.State,
			Reason:            view.Pause.Reason,
			PausedAt:          view.Pause.PausedAt,
			GraphRevision:     view.Pause.GraphRevision,
			GameID:            view.Pause.GameID,
			FrozenRemainingMS: view.Pause.FrozenRemainingMS,
			ReconnectDeadline: view.Pause.ReconnectDeadline,
		}
	}
	for index, link := range view.AuditLinks {
		input.AuditLinks[index] = tournamentws.OperatorAuditLinkInput{
			TournamentID:             view.TournamentID,
			AuditEventID:             link.AuditEventID,
			EntityKind:               link.EntityKind,
			EntityID:                 link.EntityID,
			OfficialResultRevisionID: link.OfficialResultRevisionID,
		}
	}
	return input
}

func tournamentSnapshotMetadata(
	role string,
	tournamentID uuid.UUID,
	principalID uuid.UUID,
	revision int64,
	sequence int64,
	occurredAt time.Time,
) (tournamentws.RealtimeEnvelopeMetadata, error) {
	if tournamentID == uuid.Nil || revision < 1 || sequence < 0 || occurredAt.IsZero() {
		return tournamentws.RealtimeEnvelopeMetadata{}, ErrTournamentSnapshotSource
	}
	occurredAt = occurredAt.Round(0).UTC()
	identity := fmt.Sprintf("tournament-snapshot:%s:%s:%s:%d:%d", role, tournamentID, principalID, revision, sequence)
	return tournamentws.RealtimeEnvelopeMetadata{
		SchemaVersion:      tournamentws.TournamentRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           sequence,
		EventID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)),
		OccurredAt:         occurredAt,
		ProjectionRevision: revision,
	}, nil
}

var (
	_ tournamentws.ParticipantRealtimeReadSource = (*TournamentProductionSnapshotSource)(nil)
	_ tournamentws.PublicRealtimeReadSource      = (*TournamentProductionSnapshotSource)(nil)
	_ tournamentws.OperatorRealtimeReadSource    = (*TournamentProductionSnapshotSource)(nil)
)
