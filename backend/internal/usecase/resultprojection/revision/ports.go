package revision

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

type OfficialResultStatus string

const (
	OfficialResultStatusSolved     OfficialResultStatus = "solved"
	OfficialResultStatusCompleted  OfficialResultStatus = "completed"
	OfficialResultStatusNoShow     OfficialResultStatus = "no_show"
	OfficialResultStatusVoid       OfficialResultStatus = "void"
	OfficialResultStatusCancelled  OfficialResultStatus = "cancelled"
	OfficialResultStatusSuperseded OfficialResultStatus = "superseded"
)

type OfficialResultCause string

const OfficialResultCauseNoShow OfficialResultCause = "no_show"

// TerminalResultSource makes the persisted terminal path explicit. It keeps
// a pre-start operator forfeit from being accepted as an ordinary played
// result merely because both paths end in a terminal Series revision.
type TerminalResultSource string

const (
	TerminalResultSourcePlayed          TerminalResultSource = "played"
	TerminalResultSourceNormalNoShow    TerminalResultSource = "normal_no_show"
	TerminalResultSourcePreStartForfeit TerminalResultSource = "pre_start_forfeit"
)

type PublicOfficialResult struct {
	Subject      resultusecase.OfficialResultSubjectKind `json:"subject"`
	TournamentID uuid.UUID                               `json:"tournament_id"`
	SeriesID     uuid.UUID                               `json:"series_id"`
	GameID       *uuid.UUID                              `json:"game_id"`
	Status       OfficialResultStatus                    `json:"status"`
	WinnerID     *uuid.UUID                              `json:"winner_id"`
	Score        *domain.SeriesScore                     `json:"score"`
	ResolvedAt   time.Time                               `json:"resolved_at"`
}

type OperatorOfficialResult struct {
	Public                     PublicOfficialResult             `json:"public"`
	ResultRevisionID           domain.OfficialResultRevisionID  `json:"result_revision_id"`
	PreviousResultRevisionID   *domain.OfficialResultRevisionID `json:"previous_result_revision_id"`
	ScoreRevisionID            *domain.SeriesScoreRevisionID    `json:"score_revision_id"`
	SourceProjectionRevisionID domain.DerivedRevisionID         `json:"source_projection_revision_id"`
	ScoreProjectionRevisionID  *domain.DerivedRevisionID        `json:"score_projection_revision_id"`
	GameState                  *domain.GameState                `json:"game_state"`
	GameReason                 *domain.GameResultReason         `json:"game_reason"`
	SeriesState                *domain.SeriesState              `json:"series_state"`
	SeriesReason               *domain.SeriesResultReason       `json:"series_reason"`
	Cause                      *OfficialResultCause             `json:"cause"`
	CommandID                  uuid.UUID                        `json:"command_id"`
	NoShowAction               *domain.NormalNoShowAction       `json:"no_show_action"`
}

type RecordedNoGameResult struct {
	restoredOrigin       noGameOrigin
	Scope                domain.NormalNoShowScope
	CommandID            uuid.UUID
	Action               domain.NormalNoShowAction
	Format               domain.SeriesFormat
	FirstParticipantID   uuid.UUID
	SecondParticipantID  uuid.UUID
	ReadyParticipantID   *uuid.UUID
	GameResults          []domain.NormalNoShowGameRevision
	Topology             []RecordedNoGameAttempt
	Score                domain.NormalNoShowScoreRevision
	Series               domain.NormalNoShowSeriesRevision
	GameSourceRevisions  []domain.DerivedRevision
	ScoreSourceRevision  domain.DerivedRevision
	ResultSourceRevision domain.DerivedRevision
	GameProjections      []domain.ProjectionRevision
	GameDependencies     []domain.RevisionDependency
	ScoreProjection      domain.ProjectionRevision
	ResultProjection     domain.ProjectionRevision
	ResultDependency     domain.RevisionDependency
	ResolvedAt           time.Time
}

type RecordedNoGameAttempt struct {
	SeriesID         uuid.UUID
	SlotID           uuid.UUID
	SlotPosition     int
	GameID           uuid.UUID
	AttemptNo        int
	ResultRevisionID domain.OfficialResultRevisionID
}

type OfficialResultProjectionInput struct {
	TerminalSource   TerminalResultSource
	Result           resultusecase.OfficialResultRevisionHead
	ResultProjection domain.ProjectionRevision
	Score            *resultusecase.SeriesScoreRevisionHead
	ScoreProjection  *domain.ProjectionRevision
	NoGame           *RecordedNoGameResult
}

func (i OfficialResultProjectionInput) Clone() (OfficialResultProjectionInput, error) {
	return cloneOfficialResultProjectionInput(i)
}

type OfficialResultProjectionPlan struct {
	input    OfficialResultProjectionInput
	public   PublicOfficialResult
	operator OperatorOfficialResult
}
