package websocket

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

const (
	EventQueueJoined          = "queue_joined"
	EventQueueLeft            = "queue_left"
	EventMatchFound           = "match_found"
	EventTaskAssigned         = "task_assigned"
	EventFlagResult           = "flag_result"
	EventHintUnlocked         = "hint_unlocked"
	EventDuelExpired          = "duel_expired"
	EventDuelFinished         = "duel_finished"
	EventOpponentSolved       = "opponent_solved"
	EventOpponentDisconnected = "opponent_disconnected"
	EventOpponentReconnected  = "opponent_reconnected"
	EventDuelResume           = "duel_resume"
	EventPong                 = "pong"
	EventError                = "error"

	EventJoinQueue  = "join_queue"
	EventLeaveQueue = "leave_queue"
	EventFlagSubmit = "flag_submit"
	EventSurrender  = "surrender"
	EventPing       = "ping"
)

const (
	ErrorUnknownEvent   = "unknown_event"
	ErrorInvalidJSON    = "invalid_json"
	ErrorInvalidPayload = "invalid_payload"
	ErrorServerShutdown = "server_shutdown"
	ErrorInternal       = "internal"
	ErrorDuelPaused     = "duel.paused"
)

type IncomingEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type DuelPayload struct {
	ID         uuid.UUID  `json:"id"`
	Player1ID  uuid.UUID  `json:"player1_id"`
	Player2ID  uuid.UUID  `json:"player2_id"`
	Status     string     `json:"status"`
	WinnerID   *uuid.UUID `json:"winner_id,omitempty"`
	Deadline   time.Time  `json:"deadline"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type TaskPayload struct {
	ID            uuid.UUID           `json:"id"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	Category      string              `json:"category"`
	Difficulty    string              `json:"difficulty"`
	TimeLimit     int                 `json:"time_limit"`
	TimeLimitSec  int                 `json:"time_limit_seconds"`
	TaskURL       *string             `json:"task_url,omitempty"`
	SourceURL     *string             `json:"source_url,omitempty"`
	SourceFileURL *string             `json:"source_file_url,omitempty"`
	HintSchedule  []HintScheduleEntry `json:"hint_schedule,omitempty"`
	UnlockedHints []UnlockedHint      `json:"unlocked_hints,omitempty"`
}

type MatchFoundPayload struct {
	DuelID           uuid.UUID   `json:"duel_id"`
	OpponentUsername string      `json:"opponent_username"`
	Duel             DuelPayload `json:"duel"`
}

type TaskAssignedPayload struct {
	DuelID           uuid.UUID   `json:"duel_id"`
	Deadline         time.Time   `json:"deadline"`
	TimeLimitSeconds int         `json:"time_limit_seconds"`
	Task             TaskPayload `json:"task"`
}

type FlagResultPayload struct {
	DuelID  uuid.UUID `json:"duel_id"`
	Correct bool      `json:"correct"`
	Message string    `json:"message,omitempty"`
}

type DuelFinishedPayload struct {
	DuelID         uuid.UUID   `json:"duel_id"`
	WinnerID       *uuid.UUID  `json:"winner_id,omitempty"`
	WinnerUsername *string     `json:"winner_username,omitempty"`
	YourSolved     bool        `json:"your_solved"`
	OpponentSolved bool        `json:"opponent_solved"`
	Duel           DuelPayload `json:"duel"`
}

type DuelExpiredPayload struct {
	DuelID uuid.UUID `json:"duel_id"`
}

type HintScheduleEntry struct {
	HintIndex int       `json:"hint_index"`
	UnlockAt  time.Time `json:"unlock_at"`
}

type UnlockedHint struct {
	HintIndex  int       `json:"hint_index"`
	Hint       string    `json:"hint"`
	UnlockedAt time.Time `json:"unlocked_at"`
}

type HintUnlockedPayload struct {
	DuelID     uuid.UUID `json:"duel_id"`
	TaskID     uuid.UUID `json:"task_id"`
	HintIndex  int       `json:"hint_index"`
	Hint       string    `json:"hint"`
	UnlockedAt time.Time `json:"unlocked_at"`
}

type OpponentSolvedPayload struct {
	DuelID   uuid.UUID `json:"duel_id"`
	PlayerID uuid.UUID `json:"player_id"`
}

type OpponentDisconnectedPayload struct {
	DuelID            uuid.UUID `json:"duel_id"`
	PlayerID          uuid.UUID `json:"player_id"`
	ReconnectDeadline time.Time `json:"reconnect_deadline"`
}

type OpponentReconnectedPayload struct {
	DuelID   uuid.UUID `json:"duel_id"`
	PlayerID uuid.UUID `json:"player_id"`
	Deadline time.Time `json:"deadline"`
}

type DuelResumePayload struct {
	DuelID                    uuid.UUID    `json:"duel_id"`
	Deadline                  time.Time    `json:"deadline"`
	OpponentDisconnected      bool         `json:"opponent_disconnected,omitempty"`
	OpponentReconnectDeadline *time.Time   `json:"opponent_reconnect_deadline,omitempty"`
	Task                      *TaskPayload `json:"task,omitempty"`
}

func marshalEvent(typ string, payload any) ([]byte, error) {
	return json.Marshal(Event{Type: typ, Payload: payload})
}

func marshalError(code, message string) ([]byte, error) {
	return json.Marshal(Event{Type: EventError, Code: code, Message: message})
}

func duelPayload(duel *domain.Duel) DuelPayload {
	return DuelPayload{
		ID:         duel.ID,
		Player1ID:  duel.Player1ID,
		Player2ID:  duel.Player2ID,
		Status:     duel.Status.String(),
		WinnerID:   duel.WinnerID,
		Deadline:   duel.Deadline,
		StartedAt:  duel.StartedAt,
		FinishedAt: duel.FinishedAt,
	}
}

func taskPayload(task *domain.Task, hints duelusecase.HintSnapshot) TaskPayload {
	return TaskPayload{
		ID:            task.ID,
		Title:         task.Title,
		Description:   task.Description,
		Category:      task.Category.String(),
		Difficulty:    task.Difficulty.String(),
		TimeLimit:     task.TimeLimit,
		TimeLimitSec:  task.TimeLimit,
		TaskURL:       task.TaskURL,
		SourceURL:     task.SourceFileURL,
		SourceFileURL: task.SourceFileURL,
		HintSchedule:  hintSchedulePayload(hints.Schedule),
		UnlockedHints: unlockedHintsPayload(hints.Unlocked),
	}
}

func duelFinishedPayload(
	duel *domain.Duel,
	recipientID uuid.UUID,
	solvedPlayerID *uuid.UUID,
	winnerUsername *string,
) DuelFinishedPayload {
	opponent, _ := duelOpponentID(duel, recipientID)
	yourSolved := solvedPlayerID != nil && *solvedPlayerID == recipientID
	opponentSolved := solvedPlayerID != nil && *solvedPlayerID == opponent
	return DuelFinishedPayload{
		DuelID:         duel.ID,
		WinnerID:       duel.WinnerID,
		WinnerUsername: winnerUsername,
		YourSolved:     yourSolved,
		OpponentSolved: opponentSolved,
		Duel:           duelPayload(duel),
	}
}

func duelOpponentID(duel *domain.Duel, playerID uuid.UUID) (uuid.UUID, bool) {
	if duel == nil {
		return uuid.Nil, false
	}
	switch playerID {
	case duel.Player1ID:
		return duel.Player2ID, true
	case duel.Player2ID:
		return duel.Player1ID, true
	default:
		return uuid.Nil, false
	}
}

func hintSchedulePayload(schedule []domain.HintScheduleEntry) []HintScheduleEntry {
	out := make([]HintScheduleEntry, 0, len(schedule))
	for _, entry := range schedule {
		out = append(out, HintScheduleEntry{
			HintIndex: entry.Index,
			UnlockAt:  entry.UnlockAt,
		})
	}
	return out
}

func unlockedHintsPayload(hints []domain.UnlockedHint) []UnlockedHint {
	out := make([]UnlockedHint, 0, len(hints))
	for _, hint := range hints {
		out = append(out, UnlockedHint{
			HintIndex:  hint.Index,
			Hint:       hint.Text,
			UnlockedAt: hint.UnlockedAt,
		})
	}
	return out
}

func hintUnlockedPayload(event duelusecase.HintUnlocked) HintUnlockedPayload {
	return HintUnlockedPayload{
		DuelID:     event.DuelID,
		TaskID:     event.TaskID,
		HintIndex:  event.Hint.Index,
		Hint:       event.Hint.Text,
		UnlockedAt: event.Hint.UnlockedAt,
	}
}
