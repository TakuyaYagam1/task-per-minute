package tournament

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidParticipantSnapshot = errors.New("invalid tournament participant snapshot")

type ParticipantSnapshotScope struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
}

type ParticipantSnapshotInput struct {
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Revision     int64
	LastSequence int64
	Game         *ParticipantGameInput
	Assignment   *ParticipantAssignmentInput
	Opponent     *OpponentCompetitionInput
	Golden       *ParticipantGoldenInput
}

// ParticipantGameInput is the participant-safe game projection. It contains
// only the game identity, lifecycle state, and revision; pause details are
// limited to the reconnect clock needed by that participant.
type ParticipantGameInput struct {
	GameID   uuid.UUID
	State    string
	Revision int64
	Pause    *ParticipantGamePauseInput
}

type ParticipantGamePauseInput struct {
	PauseID           uuid.UUID
	State             string
	FrozenAt          time.Time
	FrozenRemainingMS int64
	ResumedAt         *time.Time
	ResumedDeadline   *time.Time
	ReconnectDeadline *time.Time
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

type ParticipantGoldenInput struct {
	GroupID         uuid.UUID
	GroupRevisionID uuid.UUID
	AttemptID       uuid.UUID
	RuntimeRevision int64
	ReadyWindowID   uuid.UUID
	State           string
	Ready           bool
	Submitted       bool
	Position        *int
	StartedAt       *time.Time
	Deadline        *time.Time
	Task            *ParticipantGoldenTaskInput
}

type ParticipantGoldenTaskInput struct {
	AssignmentID        uuid.UUID
	SnapshotID          uuid.UUID
	TaskID              uuid.UUID
	Version             int
	Title               string
	Description         string
	Category            string
	Difficulty          string
	TimeLimitSeconds    int
	TaskURL             *string
	SourceFileAvailable bool
}

type ParticipantSnapshot struct {
	TournamentID uuid.UUID                 `json:"tournament_id"`
	PlayerID     uuid.UUID                 `json:"player_id"`
	Revision     int64                     `json:"revision"`
	LastSequence int64                     `json:"last_sequence"`
	Game         *ParticipantGame          `json:"game,omitempty"`
	Assignment   *ParticipantAssignment    `json:"assignment,omitempty"`
	Opponent     *OpponentCompetitionState `json:"opponent,omitempty"`
	Golden       *ParticipantGolden        `json:"golden,omitempty"`
}

type ParticipantGame struct {
	GameID   uuid.UUID             `json:"game_id"`
	State    string                `json:"state"`
	Revision int64                 `json:"revision"`
	Pause    *ParticipantGamePause `json:"pause,omitempty"`
}

type ParticipantGamePause struct {
	PauseID           uuid.UUID  `json:"pause_id"`
	State             string     `json:"state"`
	FrozenAt          time.Time  `json:"frozen_at"`
	FrozenRemainingMS int64      `json:"frozen_remaining_ms"`
	ResumedAt         *time.Time `json:"resumed_at,omitempty"`
	ResumedDeadline   *time.Time `json:"resumed_deadline,omitempty"`
	ReconnectDeadline *time.Time `json:"reconnect_deadline,omitempty"`
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

type ParticipantGolden struct {
	GroupID         uuid.UUID              `json:"group_id"`
	GroupRevisionID uuid.UUID              `json:"group_revision_id"`
	AttemptID       uuid.UUID              `json:"attempt_id"`
	RuntimeRevision int64                  `json:"runtime_revision"`
	ReadyWindowID   uuid.UUID              `json:"ready_window_id"`
	State           string                 `json:"state"`
	Ready           bool                   `json:"ready"`
	Submitted       bool                   `json:"submitted"`
	Position        *int                   `json:"position,omitempty"`
	StartedAt       *time.Time             `json:"started_at,omitempty"`
	Deadline        *time.Time             `json:"deadline,omitempty"`
	Task            *ParticipantGoldenTask `json:"task,omitempty"`
}

type ParticipantGoldenTask struct {
	AssignmentID        uuid.UUID `json:"assignment_id"`
	SnapshotID          uuid.UUID `json:"snapshot_id"`
	TaskID              uuid.UUID `json:"task_id"`
	Version             int       `json:"version"`
	Title               string    `json:"title"`
	Description         string    `json:"description"`
	Category            string    `json:"category"`
	Difficulty          string    `json:"difficulty"`
	TimeLimitSeconds    int       `json:"time_limit_seconds"`
	TaskURL             *string   `json:"task_url,omitempty"`
	SourceFileAvailable bool      `json:"source_file_available"`
}

func NewParticipantSnapshot(scope ParticipantSnapshotScope, input ParticipantSnapshotInput) (ParticipantSnapshot, error) {
	if scope.TournamentID == uuid.Nil || scope.PlayerID == uuid.Nil || input.TournamentID != scope.TournamentID || input.PlayerID != scope.PlayerID {
		return ParticipantSnapshot{}, fmt.Errorf("%w: authenticated scope does not match source", ErrInvalidParticipantSnapshot)
	}
	if input.Revision < 1 || input.LastSequence < 0 {
		return ParticipantSnapshot{}, fmt.Errorf("%w: invalid cursor", ErrInvalidParticipantSnapshot)
	}

	snapshot := ParticipantSnapshot{
		TournamentID: input.TournamentID,
		PlayerID:     input.PlayerID,
		Revision:     input.Revision,
		LastSequence: input.LastSequence,
	}
	if err := setParticipantGame(&snapshot, input.Game); err != nil {
		return ParticipantSnapshot{}, err
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
	if input.Golden != nil {
		golden, err := participantGolden(*input.Golden)
		if err != nil {
			return ParticipantSnapshot{}, err
		}
		snapshot.Golden = &golden
	}
	if err := snapshot.Validate(); err != nil {
		return ParticipantSnapshot{}, err
	}
	return snapshot, nil
}

func (s ParticipantSnapshot) Validate() error {
	if s.TournamentID == uuid.Nil || s.PlayerID == uuid.Nil || s.Revision < 1 || s.LastSequence < 0 {
		return fmt.Errorf("%w: invalid identity or cursor", ErrInvalidParticipantSnapshot)
	}
	if err := validateParticipantGame(s.Game); err != nil {
		return err
	}
	if s.Assignment != nil {
		if err := s.Assignment.validate(); err != nil {
			return err
		}
	}
	if s.Opponent != nil {
		if !validRealtimeString(s.Opponent.DisplayName) || !validRealtimeString(s.Opponent.SeriesState) || s.Opponent.Score < 0 {
			return fmt.Errorf("%w: invalid opponent competition state", ErrInvalidParticipantSnapshot)
		}
	}
	if s.Golden != nil && !validParticipantGolden(*s.Golden) {
		return fmt.Errorf("%w: invalid Golden state", ErrInvalidParticipantSnapshot)
	}
	if !valueWithinWireLimits(s) {
		return fmt.Errorf("%w: snapshot exceeds wire limits", ErrInvalidParticipantSnapshot)
	}
	return nil
}

func (s ParticipantSnapshot) clone() ParticipantSnapshot {
	clone := s
	if s.Game != nil {
		game := *s.Game
		if s.Game.Pause != nil {
			pause := *s.Game.Pause
			pause.ResumedAt = cloneTime(s.Game.Pause.ResumedAt)
			pause.ResumedDeadline = cloneTime(s.Game.Pause.ResumedDeadline)
			pause.ReconnectDeadline = cloneTime(s.Game.Pause.ReconnectDeadline)
			game.Pause = &pause
		}
		clone.Game = &game
	}
	if s.Assignment != nil {
		assignment := *s.Assignment
		clone.Assignment = &assignment
	}
	if s.Opponent != nil {
		opponent := *s.Opponent
		clone.Opponent = &opponent
	}
	if s.Golden != nil {
		golden := *s.Golden
		golden.Position = cloneInt(s.Golden.Position)
		golden.StartedAt = cloneTime(s.Golden.StartedAt)
		golden.Deadline = cloneTime(s.Golden.Deadline)
		if s.Golden.Task != nil {
			task := *s.Golden.Task
			task.TaskURL = cloneString(s.Golden.Task.TaskURL)
			golden.Task = &task
		}
		clone.Golden = &golden
	}
	return clone
}

func participantGame(input ParticipantGameInput) (ParticipantGame, error) {
	game := ParticipantGame{
		GameID:   input.GameID,
		State:    input.State,
		Revision: input.Revision,
	}
	if input.Pause != nil {
		game.Pause = &ParticipantGamePause{
			PauseID:           input.Pause.PauseID,
			State:             input.Pause.State,
			FrozenAt:          input.Pause.FrozenAt,
			FrozenRemainingMS: input.Pause.FrozenRemainingMS,
			ResumedAt:         cloneTime(input.Pause.ResumedAt),
			ResumedDeadline:   cloneTime(input.Pause.ResumedDeadline),
			ReconnectDeadline: cloneTime(input.Pause.ReconnectDeadline),
		}
	}
	if err := game.validate(); err != nil {
		return ParticipantGame{}, err
	}
	return game, nil
}

func setParticipantGame(snapshot *ParticipantSnapshot, input *ParticipantGameInput) error {
	if input == nil {
		return nil
	}
	game, err := participantGame(*input)
	if err != nil {
		return err
	}
	snapshot.Game = &game
	return nil
}

func validateParticipantGame(game *ParticipantGame) error {
	if game == nil {
		return nil
	}
	return game.validate()
}

//nolint:gocyclo // The wire boundary validates all clock-field combinations fail-closed.
func (game ParticipantGame) validate() error {
	if game.GameID == uuid.Nil || !validRealtimeString(game.State) || game.Revision < 1 {
		return fmt.Errorf("%w: invalid game state", ErrInvalidParticipantSnapshot)
	}
	if game.Pause == nil {
		return nil
	}
	if game.Pause.PauseID == uuid.Nil || !validRealtimeString(game.Pause.State) ||
		!isServerUTC(game.Pause.FrozenAt) || game.Pause.FrozenRemainingMS <= 0 ||
		!validOptionalUTC(game.Pause.ResumedAt) || !validOptionalUTC(game.Pause.ResumedDeadline) ||
		!validOptionalUTC(game.Pause.ReconnectDeadline) {
		return fmt.Errorf("%w: invalid game pause", ErrInvalidParticipantSnapshot)
	}
	if (game.Pause.ResumedAt == nil) != (game.Pause.ResumedDeadline == nil) {
		return fmt.Errorf("%w: resumed game pause clock is incomplete", ErrInvalidParticipantSnapshot)
	}
	if game.Pause.ResumedAt != nil && !game.Pause.ResumedDeadline.After(*game.Pause.ResumedAt) {
		return fmt.Errorf("%w: resumed game pause deadline is invalid", ErrInvalidParticipantSnapshot)
	}
	if game.Pause.ReconnectDeadline != nil && !game.Pause.ReconnectDeadline.After(game.Pause.FrozenAt) {
		return fmt.Errorf("%w: reconnect deadline is invalid", ErrInvalidParticipantSnapshot)
	}
	return nil
}

func participantGolden(input ParticipantGoldenInput) (ParticipantGolden, error) {
	golden := ParticipantGolden{
		GroupID: input.GroupID, GroupRevisionID: input.GroupRevisionID, AttemptID: input.AttemptID,
		RuntimeRevision: input.RuntimeRevision, ReadyWindowID: input.ReadyWindowID,
		State: input.State, Ready: input.Ready, Submitted: input.Submitted,
		Position: cloneInt(input.Position), StartedAt: cloneTime(input.StartedAt), Deadline: cloneTime(input.Deadline),
	}
	if input.Task != nil {
		golden.Task = &ParticipantGoldenTask{
			AssignmentID: input.Task.AssignmentID, SnapshotID: input.Task.SnapshotID, TaskID: input.Task.TaskID,
			Version: input.Task.Version, Title: input.Task.Title, Description: input.Task.Description,
			Category: input.Task.Category, Difficulty: input.Task.Difficulty,
			TimeLimitSeconds: input.Task.TimeLimitSeconds, TaskURL: cloneString(input.Task.TaskURL),
			SourceFileAvailable: input.Task.SourceFileAvailable,
		}
	}
	if !validParticipantGolden(golden) {
		return ParticipantGolden{}, fmt.Errorf("%w: invalid Golden state", ErrInvalidParticipantSnapshot)
	}
	return golden, nil
}

func validParticipantGolden(golden ParticipantGolden) bool {
	if !validParticipantGoldenIdentity(golden) {
		return false
	}
	if golden.Task == nil {
		return golden.StartedAt == nil && golden.Deadline == nil
	}
	if golden.StartedAt == nil || golden.Deadline == nil {
		return false
	}
	return golden.Deadline.Equal(golden.StartedAt.Add(180*time.Second)) && validParticipantGoldenTask(*golden.Task)
}

func validParticipantGoldenTask(task ParticipantGoldenTask) bool {
	return task.AssignmentID != uuid.Nil && task.SnapshotID != uuid.Nil && task.TaskID != uuid.Nil &&
		task.Version >= 1 && validRealtimeString(task.Title) && validRealtimeString(task.Description) &&
		validRealtimeString(task.Category) && validRealtimeString(task.Difficulty) && task.TimeLimitSeconds == 180 &&
		(task.TaskURL == nil || validRealtimeString(*task.TaskURL))
}

//nolint:gocyclo // Wire validation keeps the full Golden identity invariant visible in one guard.
func validParticipantGoldenIdentity(golden ParticipantGolden) bool {
	if golden.GroupID == uuid.Nil || golden.GroupRevisionID == uuid.Nil || golden.AttemptID == uuid.Nil ||
		golden.RuntimeRevision < 1 || golden.ReadyWindowID == uuid.Nil ||
		!validRealtimeString(golden.State) || (golden.Position != nil && (*golden.Position < 1 || *golden.Position > 16)) ||
		!validOptionalUTC(golden.StartedAt) || !validOptionalUTC(golden.Deadline) {
		return false
	}
	if (golden.StartedAt == nil) != (golden.Deadline == nil) {
		return false
	}
	if golden.StartedAt == nil && golden.State != "prepared" && golden.State != "ready" {
		return false
	}
	return true
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
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
	if a.Task.SnapshotID == uuid.Nil || a.Task.TaskID == uuid.Nil || !validRealtimeString(a.Task.Title) || !validRealtimeString(a.Task.Category) || !validRealtimeString(a.Task.Difficulty) || a.Task.TimeLimitSeconds < 1 {
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
	if !validRealtimeString(opponent.DisplayName) || !validRealtimeString(opponent.SeriesState) || opponent.Score < 0 {
		return OpponentCompetitionState{}, fmt.Errorf("%w: invalid opponent competition state", ErrInvalidParticipantSnapshot)
	}
	return opponent, nil
}
