package tournament

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidPublicSnapshot = errors.New("invalid tournament public snapshot")

type PublicSnapshotInput struct {
	Revision        int64
	LastSequence    int64
	Tournament      PublicTournamentInput
	Scoreboard      []PublicScoreboardEntryInput
	Bracket         []PublicBracketMatchInput
	LiveSeries      []PublicSeriesInput
	OfficialResults []PublicOfficialResultInput
	Draft           *PublicDraftInput
}

type PublicTournamentInput struct {
	TournamentID uuid.UUID
	Preset       string
	State        string
	RosterSize   int
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

type PublicScoreboardEntryInput struct {
	TournamentID    uuid.UUID
	Rank            int
	DisplayName     string
	Points          int
	Buchholz        int
	EffectiveTimeMS int64
}

type PublicBracketMatchInput struct {
	TournamentID      uuid.UUID
	Stage             string
	Position          int
	FirstDisplayName  string
	SecondDisplayName string
	FirstWins         int
	SecondWins        int
	State             string
	ScheduledAt       *time.Time
}

type PublicSeriesInput struct {
	TournamentID        uuid.UUID
	SeriesID            uuid.UUID
	Stage               string
	RoundNumber         *int
	Format              string
	State               string
	FirstDisplayName    string
	SecondDisplayName   string
	FirstWins           int
	SecondWins          int
	CurrentGamePosition int
	ScheduledAt         *time.Time
}

type PublicOfficialResultInput struct {
	TournamentID      uuid.UUID
	RevisionID        uuid.UUID
	SeriesID          uuid.UUID
	State             string
	WinnerDisplayName string
	FirstWins         int
	SecondWins        int
	RecordedAt        time.Time
}

type PublicDraftInput struct {
	TournamentID       uuid.UUID
	SeriesID           uuid.UUID
	Format             string
	State              string
	Pool               []string
	SelectedCategories []string
	Actions            []PublicDraftActionInput
}

type PublicDraftActionInput struct {
	Turn             int
	Action           string
	Category         string
	ActorDisplayName string
	OccurredAt       time.Time
}

type PublicSnapshot struct {
	Revision        int64                   `json:"revision"`
	LastSequence    int64                   `json:"last_sequence"`
	Tournament      PublicTournament        `json:"tournament"`
	Scoreboard      []PublicScoreboardEntry `json:"scoreboard"`
	Bracket         []PublicBracketMatch    `json:"bracket"`
	LiveSeries      []PublicSeries          `json:"live_series"`
	OfficialResults []PublicOfficialResult  `json:"official_results"`
	Draft           *PublicDraft            `json:"draft,omitempty"`
}

type PublicTournament struct {
	TournamentID uuid.UUID  `json:"tournament_id"`
	Preset       string     `json:"preset"`
	State        string     `json:"state"`
	RosterSize   int        `json:"roster_size"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

type PublicScoreboardEntry struct {
	Rank            int    `json:"rank"`
	DisplayName     string `json:"display_name"`
	Points          int    `json:"points"`
	Buchholz        int    `json:"buchholz"`
	EffectiveTimeMS int64  `json:"effective_time_ms"`
}

type PublicSeriesScore struct {
	FirstWins  int `json:"first_wins"`
	SecondWins int `json:"second_wins"`
}

type PublicBracketMatch struct {
	Stage             string            `json:"stage"`
	Position          int               `json:"position"`
	FirstDisplayName  string            `json:"first_display_name"`
	SecondDisplayName string            `json:"second_display_name"`
	Score             PublicSeriesScore `json:"score"`
	State             string            `json:"state"`
	ScheduledAt       *time.Time        `json:"scheduled_at"`
}

type PublicSeries struct {
	SeriesID            uuid.UUID         `json:"series_id"`
	Stage               string            `json:"stage"`
	RoundNumber         *int              `json:"round_number"`
	Format              string            `json:"format"`
	State               string            `json:"state"`
	FirstDisplayName    string            `json:"first_display_name"`
	SecondDisplayName   string            `json:"second_display_name"`
	Score               PublicSeriesScore `json:"score"`
	CurrentGamePosition int               `json:"current_game_position,omitempty"`
	ScheduledAt         *time.Time        `json:"scheduled_at"`
}

type PublicOfficialResult struct {
	RevisionID        uuid.UUID         `json:"revision_id"`
	SeriesID          uuid.UUID         `json:"series_id"`
	State             string            `json:"state"`
	WinnerDisplayName string            `json:"winner_display_name,omitempty"`
	Score             PublicSeriesScore `json:"score"`
	RecordedAt        time.Time         `json:"recorded_at"`
}

type PublicDraft struct {
	SeriesID           uuid.UUID           `json:"series_id"`
	Format             string              `json:"format"`
	State              string              `json:"state"`
	Pool               []string            `json:"pool"`
	SelectedCategories []string            `json:"selected_categories"`
	Actions            []PublicDraftAction `json:"actions"`
}

type PublicDraftAction struct {
	Turn             int       `json:"turn"`
	Action           string    `json:"action"`
	Category         string    `json:"category"`
	ActorDisplayName string    `json:"actor_display_name"`
	OccurredAt       time.Time `json:"occurred_at"`
}

//nolint:gocyclo // The constructor copies each public allowlist projection and enforces one tournament scope.
func NewPublicSnapshot(tournamentID uuid.UUID, input PublicSnapshotInput) (PublicSnapshot, error) {
	if tournamentID == uuid.Nil || input.Revision < 1 || input.LastSequence < 0 || input.Tournament.TournamentID != tournamentID {
		return PublicSnapshot{}, fmt.Errorf("%w: invalid scope or cursor", ErrInvalidPublicSnapshot)
	}
	snapshot := PublicSnapshot{
		Revision:     input.Revision,
		LastSequence: input.LastSequence,
		Tournament: PublicTournament{
			TournamentID: input.Tournament.TournamentID,
			Preset:       input.Tournament.Preset,
			State:        input.Tournament.State,
			RosterSize:   input.Tournament.RosterSize,
			StartedAt:    cloneTime(input.Tournament.StartedAt),
			FinishedAt:   cloneTime(input.Tournament.FinishedAt),
		},
		Scoreboard:      make([]PublicScoreboardEntry, len(input.Scoreboard)),
		Bracket:         make([]PublicBracketMatch, len(input.Bracket)),
		LiveSeries:      make([]PublicSeries, len(input.LiveSeries)),
		OfficialResults: make([]PublicOfficialResult, len(input.OfficialResults)),
	}
	for index, entry := range input.Scoreboard {
		if entry.TournamentID != tournamentID {
			return PublicSnapshot{}, fmt.Errorf("%w: scoreboard crosses tournament", ErrInvalidPublicSnapshot)
		}
		snapshot.Scoreboard[index] = PublicScoreboardEntry{Rank: entry.Rank, DisplayName: entry.DisplayName, Points: entry.Points, Buchholz: entry.Buchholz, EffectiveTimeMS: entry.EffectiveTimeMS}
	}
	for index, match := range input.Bracket {
		if match.TournamentID != tournamentID {
			return PublicSnapshot{}, fmt.Errorf("%w: bracket crosses tournament", ErrInvalidPublicSnapshot)
		}
		snapshot.Bracket[index] = PublicBracketMatch{Stage: match.Stage, Position: match.Position, FirstDisplayName: match.FirstDisplayName, SecondDisplayName: match.SecondDisplayName, Score: PublicSeriesScore{FirstWins: match.FirstWins, SecondWins: match.SecondWins}, State: match.State, ScheduledAt: cloneTime(match.ScheduledAt)}
	}
	for index, series := range input.LiveSeries {
		if series.TournamentID != tournamentID {
			return PublicSnapshot{}, fmt.Errorf("%w: live series crosses tournament", ErrInvalidPublicSnapshot)
		}
		snapshot.LiveSeries[index] = PublicSeries{SeriesID: series.SeriesID, Stage: series.Stage, RoundNumber: cloneInt(series.RoundNumber), Format: series.Format, State: series.State, FirstDisplayName: series.FirstDisplayName, SecondDisplayName: series.SecondDisplayName, Score: PublicSeriesScore{FirstWins: series.FirstWins, SecondWins: series.SecondWins}, CurrentGamePosition: series.CurrentGamePosition, ScheduledAt: cloneTime(series.ScheduledAt)}
	}
	for index, result := range input.OfficialResults {
		if result.TournamentID != tournamentID {
			return PublicSnapshot{}, fmt.Errorf("%w: official result crosses tournament", ErrInvalidPublicSnapshot)
		}
		snapshot.OfficialResults[index] = PublicOfficialResult{RevisionID: result.RevisionID, SeriesID: result.SeriesID, State: result.State, WinnerDisplayName: result.WinnerDisplayName, Score: PublicSeriesScore{FirstWins: result.FirstWins, SecondWins: result.SecondWins}, RecordedAt: result.RecordedAt}
	}
	if input.Draft != nil {
		if input.Draft.TournamentID != tournamentID {
			return PublicSnapshot{}, fmt.Errorf("%w: draft crosses tournament", ErrInvalidPublicSnapshot)
		}
		draft := PublicDraft{
			SeriesID:           input.Draft.SeriesID,
			Format:             input.Draft.Format,
			State:              input.Draft.State,
			Pool:               append([]string{}, input.Draft.Pool...),
			SelectedCategories: append([]string{}, input.Draft.SelectedCategories...),
			Actions:            make([]PublicDraftAction, len(input.Draft.Actions)),
		}
		for index, action := range input.Draft.Actions {
			draft.Actions[index] = PublicDraftAction(action)
		}
		snapshot.Draft = &draft
	}
	if err := snapshot.Validate(); err != nil {
		return PublicSnapshot{}, err
	}
	return snapshot, nil
}

//nolint:gocyclo // The public boundary validates every explicit scoreboard, bracket, Series, result, and draft field.
func (s PublicSnapshot) Validate() error {
	if s.Revision < 1 || s.LastSequence < 0 || s.Tournament.TournamentID == uuid.Nil {
		return fmt.Errorf("%w: invalid identity or cursor", ErrInvalidPublicSnapshot)
	}
	if !validTournamentPresetState(s.Tournament.Preset, s.Tournament.State) || s.Tournament.RosterSize < 0 || !validOptionalUTC(s.Tournament.StartedAt) || !validOptionalUTC(s.Tournament.FinishedAt) {
		return fmt.Errorf("%w: invalid tournament view", ErrInvalidPublicSnapshot)
	}
	if s.Tournament.StartedAt != nil && s.Tournament.FinishedAt != nil && s.Tournament.FinishedAt.Before(*s.Tournament.StartedAt) {
		return fmt.Errorf("%w: tournament finish precedes start", ErrInvalidPublicSnapshot)
	}
	if s.Scoreboard == nil || s.Bracket == nil || s.LiveSeries == nil || s.OfficialResults == nil ||
		!validRealtimeCollections(len(s.Scoreboard), len(s.Bracket), len(s.LiveSeries), len(s.OfficialResults)) {
		return fmt.Errorf("%w: public collections must be arrays", ErrInvalidPublicSnapshot)
	}
	for _, entry := range s.Scoreboard {
		if entry.Rank < 1 || !validRealtimeString(entry.DisplayName) || entry.Points < 0 || entry.Buchholz < 0 || entry.EffectiveTimeMS < 0 {
			return fmt.Errorf("%w: invalid scoreboard entry", ErrInvalidPublicSnapshot)
		}
	}
	for _, match := range s.Bracket {
		if match.Position < 1 || !validPublicLabels(match.Stage, match.FirstDisplayName, match.SecondDisplayName, match.State) || !match.Score.valid() || !validOptionalUTC(match.ScheduledAt) {
			return fmt.Errorf("%w: invalid bracket match", ErrInvalidPublicSnapshot)
		}
	}
	for _, series := range s.LiveSeries {
		if series.SeriesID == uuid.Nil || !validPublicStage(series.Stage) || (series.Stage == "swiss" && (series.RoundNumber == nil || *series.RoundNumber < 1 || *series.RoundNumber > 4)) || (series.Stage != "swiss" && series.RoundNumber != nil) || series.CurrentGamePosition < 0 || !validPublicLabels(series.Format, series.State, series.FirstDisplayName, series.SecondDisplayName) || !series.Score.valid() || !validOptionalUTC(series.ScheduledAt) {
			return fmt.Errorf("%w: invalid live series", ErrInvalidPublicSnapshot)
		}
	}
	for _, result := range s.OfficialResults {
		if result.RevisionID == uuid.Nil || result.SeriesID == uuid.Nil || !validRealtimeString(result.State) || !validOptionalRealtimeString(result.WinnerDisplayName) || !result.Score.valid() || !isServerUTC(result.RecordedAt) {
			return fmt.Errorf("%w: invalid official result", ErrInvalidPublicSnapshot)
		}
	}
	if s.Draft != nil {
		if err := s.Draft.validate(); err != nil {
			return err
		}
	}
	if !valueWithinWireLimits(s) {
		return fmt.Errorf("%w: snapshot exceeds wire limits", ErrInvalidPublicSnapshot)
	}
	return nil
}

func (s PublicSnapshot) clone() PublicSnapshot {
	clone := s
	clone.Tournament.StartedAt = cloneTime(s.Tournament.StartedAt)
	clone.Tournament.FinishedAt = cloneTime(s.Tournament.FinishedAt)
	clone.Scoreboard = append([]PublicScoreboardEntry{}, s.Scoreboard...)
	clone.Bracket = append([]PublicBracketMatch{}, s.Bracket...)
	clone.LiveSeries = append([]PublicSeries{}, s.LiveSeries...)
	for index := range clone.Bracket {
		clone.Bracket[index].ScheduledAt = cloneTime(s.Bracket[index].ScheduledAt)
	}
	for index := range clone.LiveSeries {
		clone.LiveSeries[index].RoundNumber = cloneInt(s.LiveSeries[index].RoundNumber)
		clone.LiveSeries[index].ScheduledAt = cloneTime(s.LiveSeries[index].ScheduledAt)
	}
	clone.OfficialResults = append([]PublicOfficialResult{}, s.OfficialResults...)
	if s.Draft != nil {
		draft := *s.Draft
		draft.Pool = append([]string{}, s.Draft.Pool...)
		draft.SelectedCategories = append([]string{}, s.Draft.SelectedCategories...)
		draft.Actions = append([]PublicDraftAction{}, s.Draft.Actions...)
		clone.Draft = &draft
	}
	return clone
}

func (s PublicSeriesScore) valid() bool {
	return s.FirstWins >= 0 && s.SecondWins >= 0
}

func (d PublicDraft) validate() error {
	if d.SeriesID == uuid.Nil || !validPublicLabels(d.Format, d.State) || d.Pool == nil || d.SelectedCategories == nil || d.Actions == nil ||
		!validRealtimeCollections(len(d.Pool), len(d.SelectedCategories), len(d.Actions)) {
		return fmt.Errorf("%w: invalid draft view", ErrInvalidPublicSnapshot)
	}
	for _, value := range append(append([]string{}, d.Pool...), d.SelectedCategories...) {
		if !validRealtimeString(value) {
			return fmt.Errorf("%w: empty draft category", ErrInvalidPublicSnapshot)
		}
	}
	for _, action := range d.Actions {
		if action.Turn < 1 || !validPublicLabels(action.Action, action.Category, action.ActorDisplayName) || !isServerUTC(action.OccurredAt) {
			return fmt.Errorf("%w: invalid draft action", ErrInvalidPublicSnapshot)
		}
	}
	return nil
}

func validPublicLabels(values ...string) bool {
	for _, value := range values {
		if !validRealtimeString(value) {
			return false
		}
	}
	return true
}

func validPublicStage(value string) bool {
	switch value {
	case "swiss", "golden", "semifinal", "final":
		return true
	default:
		return false
	}
}
