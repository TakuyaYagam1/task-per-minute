package arena

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

var ErrInvalidParticipantSnapshot = errors.New("invalid Arena participant snapshot")

type ParticipantSnapshotScope struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
}

type ParticipantSnapshotInput struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Revision     int64
	LastSequence int64
	Assignment   *ParticipantAssignmentInput
	Opponent     *OpponentCompetitionInput
}

type ParticipantAssignmentInput struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	AssignmentID uuid.UUID
	AttemptID    uuid.UUID
	SeriesID     uuid.UUID
	GameID       uuid.UUID
	WaveID       uuid.UUID
	Task         ParticipantTaskInput
}

type ParticipantTaskInput struct {
	SnapshotID       uuid.UUID
	TaskID           uuid.UUID
	Title            string
	Category         string
	Difficulty       string
	TimeLimitSeconds int
}

type OpponentCompetitionInput struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	DisplayName  string
	SeriesID     uuid.UUID
	Ready        bool
	SeriesState  string
	Score        int
}

type ParticipantSnapshot struct {
	TournamentID uuid.UUID                 `json:"tournament_id"`
	PlayerID     uuid.UUID                 `json:"player_id"`
	Revision     int64                     `json:"revision"`
	LastSequence int64                     `json:"last_sequence"`
	Assignment   *ParticipantAssignment    `json:"assignment,omitempty"`
	Opponent     *OpponentCompetitionState `json:"opponent,omitempty"`
}

type ParticipantAssignment struct {
	AssignmentID uuid.UUID       `json:"assignment_id"`
	AttemptID    uuid.UUID       `json:"attempt_id"`
	SeriesID     uuid.UUID       `json:"series_id"`
	GameID       uuid.UUID       `json:"game_id"`
	WaveID       uuid.UUID       `json:"wave_id"`
	Task         ParticipantTask `json:"task"`
}

type ParticipantTask struct {
	SnapshotID       uuid.UUID `json:"snapshot_id"`
	TaskID           uuid.UUID `json:"task_id"`
	Title            string    `json:"title"`
	Category         string    `json:"category"`
	Difficulty       string    `json:"difficulty"`
	TimeLimitSeconds int       `json:"time_limit_seconds"`
}

type OpponentCompetitionState struct {
	DisplayName string `json:"display_name"`
	Ready       bool   `json:"ready"`
	SeriesState string `json:"series_state"`
	Score       int    `json:"score"`
}

func NewParticipantSnapshot(scope ParticipantSnapshotScope, input ParticipantSnapshotInput) (ParticipantSnapshot, error) {
	if scope.TournamentID == uuid.Nil || scope.PlayerID == uuid.Nil || input.TournamentID != scope.TournamentID || input.PlayerID != scope.PlayerID {
		return ParticipantSnapshot{}, fmt.Errorf("%w: authenticated scope does not match source", ErrInvalidParticipantSnapshot)
	}
	if input.Revision < 1 || input.LastSequence < 1 {
		return ParticipantSnapshot{}, fmt.Errorf("%w: invalid cursor", ErrInvalidParticipantSnapshot)
	}

	snapshot := ParticipantSnapshot{
		TournamentID: input.TournamentID,
		PlayerID:     input.PlayerID,
		Revision:     input.Revision,
		LastSequence: input.LastSequence,
	}
	if input.Assignment != nil {
		assignment, err := participantAssignment(scope, *input.Assignment)
		if err != nil {
			return ParticipantSnapshot{}, err
		}
		snapshot.Assignment = &assignment
	}
	if input.Opponent != nil {
		opponent, err := opponentCompetition(scope, snapshot.Assignment, *input.Opponent)
		if err != nil {
			return ParticipantSnapshot{}, err
		}
		snapshot.Opponent = &opponent
	}
	if err := snapshot.Validate(); err != nil {
		return ParticipantSnapshot{}, err
	}
	return snapshot, nil
}

func (s ParticipantSnapshot) Validate() error {
	if s.TournamentID == uuid.Nil || s.PlayerID == uuid.Nil || s.Revision < 1 || s.LastSequence < 1 {
		return fmt.Errorf("%w: invalid identity or cursor", ErrInvalidParticipantSnapshot)
	}
	if s.Assignment != nil {
		if err := s.Assignment.validate(); err != nil {
			return err
		}
	}
	if s.Opponent != nil {
		if strings.TrimSpace(s.Opponent.DisplayName) == "" || strings.TrimSpace(s.Opponent.SeriesState) == "" || s.Opponent.Score < 0 {
			return fmt.Errorf("%w: invalid opponent competition state", ErrInvalidParticipantSnapshot)
		}
	}
	return nil
}

func (s ParticipantSnapshot) clone() ParticipantSnapshot {
	clone := s
	if s.Assignment != nil {
		assignment := *s.Assignment
		clone.Assignment = &assignment
	}
	if s.Opponent != nil {
		opponent := *s.Opponent
		clone.Opponent = &opponent
	}
	return clone
}

func participantAssignment(scope ParticipantSnapshotScope, input ParticipantAssignmentInput) (ParticipantAssignment, error) {
	if input.TournamentID != scope.TournamentID || input.PlayerID != scope.PlayerID {
		return ParticipantAssignment{}, fmt.Errorf("%w: assignment is outside authenticated scope", ErrInvalidParticipantSnapshot)
	}
	assignment := ParticipantAssignment{
		AssignmentID: input.AssignmentID,
		AttemptID:    input.AttemptID,
		SeriesID:     input.SeriesID,
		GameID:       input.GameID,
		WaveID:       input.WaveID,
		Task: ParticipantTask{
			SnapshotID:       input.Task.SnapshotID,
			TaskID:           input.Task.TaskID,
			Title:            input.Task.Title,
			Category:         input.Task.Category,
			Difficulty:       input.Task.Difficulty,
			TimeLimitSeconds: input.Task.TimeLimitSeconds,
		},
	}
	if err := assignment.validate(); err != nil {
		return ParticipantAssignment{}, err
	}
	return assignment, nil
}

func (a ParticipantAssignment) validate() error {
	if a.AssignmentID == uuid.Nil || a.AttemptID == uuid.Nil || a.SeriesID == uuid.Nil || a.GameID == uuid.Nil || a.WaveID == uuid.Nil {
		return fmt.Errorf("%w: incomplete assignment identity", ErrInvalidParticipantSnapshot)
	}
	if a.Task.SnapshotID == uuid.Nil || a.Task.TaskID == uuid.Nil || strings.TrimSpace(a.Task.Title) == "" || strings.TrimSpace(a.Task.Category) == "" || strings.TrimSpace(a.Task.Difficulty) == "" || a.Task.TimeLimitSeconds < 1 {
		return fmt.Errorf("%w: invalid public task view", ErrInvalidParticipantSnapshot)
	}
	return nil
}

func opponentCompetition(scope ParticipantSnapshotScope, assignment *ParticipantAssignment, input OpponentCompetitionInput) (OpponentCompetitionState, error) {
	if input.TournamentID != scope.TournamentID || input.PlayerID == uuid.Nil || input.PlayerID == scope.PlayerID {
		return OpponentCompetitionState{}, fmt.Errorf("%w: opponent is outside authenticated competition", ErrInvalidParticipantSnapshot)
	}
	if assignment != nil && input.SeriesID != assignment.SeriesID {
		return OpponentCompetitionState{}, fmt.Errorf("%w: opponent belongs to another series", ErrInvalidParticipantSnapshot)
	}
	opponent := OpponentCompetitionState{
		DisplayName: input.DisplayName,
		Ready:       input.Ready,
		SeriesState: input.SeriesState,
		Score:       input.Score,
	}
	if strings.TrimSpace(opponent.DisplayName) == "" || strings.TrimSpace(opponent.SeriesState) == "" || opponent.Score < 0 {
		return OpponentCompetitionState{}, fmt.Errorf("%w: invalid opponent competition state", ErrInvalidParticipantSnapshot)
	}
	return opponent, nil
}
