package tournament

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

var ErrInvalidPublicSnapshot = errors.New("invalid tournament public snapshot")

type PublicSnapshotInput struct {
	Revision        int64
	LastSequence    int64
	Tournament      PublicTournamentInput
	Scoreboard      []PublicScoreboardEntryInput
	SwissRounds     []PublicSwissRoundInput
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
	TournamentID        uuid.UUID
	Rank                int
	DisplayName         string
	Points              int
	Wins                int
	Losses              int
	ByeCount            int
	Buchholz            int
	EffectiveTimeMS     int64
	ProvisionalTie      bool
	QualificationStatus string
}

type PublicSwissRoundInput struct {
	RoundNumber int
	State       string
	Bye         *PublicSwissByeInput
}

type PublicSwissByeInput struct {
	DisplayName   string
	PointsAwarded int
}

type PublicBracketMatchInput struct {
	TournamentID      uuid.UUID
	Stage             string
	Position          int
	Format            string
	FirstDisplayName  *string
	SecondDisplayName *string
	FirstWins         int
	SecondWins        int
	State             string
	ScheduledAt       *time.Time
	WinnerDisplayName *string
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
	CurrentGame         *PublicCurrentGameInput
	ScheduledAt         *time.Time
}

type PublicCurrentGameInput struct {
	Position               int
	Category               string
	State                  string
	StartedAt              *time.Time
	EffectiveDeadline      *time.Time
	FinishedAt             *time.Time
	SolveTimeMS            *int64
	ResultReason           *string
	WinnerDisplayName      *string
	FirstConnectionStatus  string
	SecondConnectionStatus string
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
	TournamentID            uuid.UUID
	SeriesID                uuid.UUID
	Format                  string
	State                   string
	FirstActorDisplayName   string
	CurrentTurn             *int
	CurrentAction           *string
	CurrentActorDisplayName *string
	TurnDeadline            *time.Time
	AutoActionPending       bool
	Pool                    []string
	SelectedCategories      []string
	Actions                 []PublicDraftActionInput
}

type PublicDraftActionInput struct {
	Turn             int
	Action           string
	Category         string
	ActorDisplayName string
	OccurredAt       time.Time
	Automatic        bool
}

type PublicSnapshot struct {
	Revision        int64                   `json:"revision"`
	LastSequence    int64                   `json:"last_sequence"`
	Tournament      PublicTournament        `json:"tournament"`
	Scoreboard      []PublicScoreboardEntry `json:"scoreboard"`
	SwissRounds     []PublicSwissRound      `json:"swiss_rounds"`
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
	Rank                int    `json:"rank"`
	DisplayName         string `json:"display_name"`
	Points              int    `json:"points"`
	Wins                int    `json:"wins"`
	Losses              int    `json:"losses"`
	ByeCount            int    `json:"bye_count"`
	Buchholz            int    `json:"buchholz"`
	EffectiveTimeMS     int64  `json:"effective_time_ms"`
	ProvisionalTie      bool   `json:"provisional_tie"`
	QualificationStatus string `json:"qualification_status"`
}

type PublicSwissRound struct {
	RoundNumber int             `json:"round_number"`
	State       string          `json:"state"`
	Bye         *PublicSwissBye `json:"bye"`
}

type PublicSwissBye struct {
	DisplayName   string `json:"display_name"`
	PointsAwarded int    `json:"points_awarded"`
}

type PublicSeriesScore struct {
	FirstWins  int `json:"first_wins"`
	SecondWins int `json:"second_wins"`
}

type PublicBracketMatch struct {
	Stage             string            `json:"stage"`
	Position          int               `json:"position"`
	Format            string            `json:"format"`
	FirstDisplayName  *string           `json:"first_display_name"`
	SecondDisplayName *string           `json:"second_display_name"`
	Score             PublicSeriesScore `json:"score"`
	State             string            `json:"state"`
	ScheduledAt       *time.Time        `json:"scheduled_at"`
	WinnerDisplayName *string           `json:"winner_display_name"`
}

type PublicSeries struct {
	SeriesID            uuid.UUID          `json:"series_id"`
	Stage               string             `json:"stage"`
	RoundNumber         *int               `json:"round_number"`
	Format              string             `json:"format"`
	State               string             `json:"state"`
	FirstDisplayName    string             `json:"first_display_name"`
	SecondDisplayName   string             `json:"second_display_name"`
	Score               PublicSeriesScore  `json:"score"`
	CurrentGamePosition int                `json:"current_game_position,omitempty"`
	CurrentGame         *PublicCurrentGame `json:"current_game"`
	ScheduledAt         *time.Time         `json:"scheduled_at"`
}

type PublicCurrentGame struct {
	Position               int        `json:"position"`
	Category               string     `json:"category"`
	State                  string     `json:"state"`
	StartedAt              *time.Time `json:"started_at"`
	EffectiveDeadline      *time.Time `json:"effective_deadline"`
	FinishedAt             *time.Time `json:"finished_at"`
	SolveTimeMS            *int64     `json:"solve_time_ms,omitempty"`
	ResultReason           *string    `json:"result_reason"`
	WinnerDisplayName      *string    `json:"winner_display_name"`
	FirstConnectionStatus  string     `json:"first_connection_status"`
	SecondConnectionStatus string     `json:"second_connection_status"`
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
	SeriesID                uuid.UUID           `json:"series_id"`
	Format                  string              `json:"format"`
	State                   string              `json:"state"`
	FirstActorDisplayName   string              `json:"first_actor_display_name"`
	CurrentTurn             *int                `json:"current_turn"`
	CurrentAction           *string             `json:"current_action"`
	CurrentActorDisplayName *string             `json:"current_actor_display_name"`
	TurnDeadline            *time.Time          `json:"turn_deadline"`
	AutoActionPending       bool                `json:"auto_action_pending"`
	Pool                    []string            `json:"pool"`
	SelectedCategories      []string            `json:"selected_categories"`
	Actions                 []PublicDraftAction `json:"actions"`
}

type PublicDraftAction struct {
	Turn             int       `json:"turn"`
	Action           string    `json:"action"`
	Category         string    `json:"category"`
	ActorDisplayName string    `json:"actor_display_name"`
	OccurredAt       time.Time `json:"occurred_at"`
	Automatic        bool      `json:"automatic"`
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
		SwissRounds:     make([]PublicSwissRound, len(input.SwissRounds)),
		Bracket:         make([]PublicBracketMatch, len(input.Bracket)),
		LiveSeries:      make([]PublicSeries, len(input.LiveSeries)),
		OfficialResults: make([]PublicOfficialResult, len(input.OfficialResults)),
	}
	for index, entry := range input.Scoreboard {
		if entry.TournamentID != tournamentID {
			return PublicSnapshot{}, fmt.Errorf("%w: scoreboard crosses tournament", ErrInvalidPublicSnapshot)
		}
		snapshot.Scoreboard[index] = PublicScoreboardEntry{
			Rank: entry.Rank, DisplayName: entry.DisplayName, Points: entry.Points,
			Wins: entry.Wins, Losses: entry.Losses, ByeCount: entry.ByeCount,
			Buchholz: entry.Buchholz, EffectiveTimeMS: entry.EffectiveTimeMS,
			ProvisionalTie: entry.ProvisionalTie, QualificationStatus: entry.QualificationStatus,
		}
	}
	for index, round := range input.SwissRounds {
		snapshot.SwissRounds[index] = PublicSwissRound{
			RoundNumber: round.RoundNumber,
			State:       round.State,
		}
		if round.Bye != nil {
			snapshot.SwissRounds[index].Bye = &PublicSwissBye{
				DisplayName:   round.Bye.DisplayName,
				PointsAwarded: round.Bye.PointsAwarded,
			}
		}
	}
	for index, match := range input.Bracket {
		if match.TournamentID != tournamentID {
			return PublicSnapshot{}, fmt.Errorf("%w: bracket crosses tournament", ErrInvalidPublicSnapshot)
		}
		snapshot.Bracket[index] = PublicBracketMatch{
			Stage:             match.Stage,
			Position:          match.Position,
			Format:            match.Format,
			FirstDisplayName:  cloneString(match.FirstDisplayName),
			SecondDisplayName: cloneString(match.SecondDisplayName),
			Score:             PublicSeriesScore{FirstWins: match.FirstWins, SecondWins: match.SecondWins},
			State:             match.State,
			ScheduledAt:       cloneTime(match.ScheduledAt),
			WinnerDisplayName: cloneString(match.WinnerDisplayName),
		}
	}
	for index, series := range input.LiveSeries {
		if series.TournamentID != tournamentID {
			return PublicSnapshot{}, fmt.Errorf("%w: live series crosses tournament", ErrInvalidPublicSnapshot)
		}
		snapshot.LiveSeries[index] = PublicSeries{SeriesID: series.SeriesID, Stage: series.Stage, RoundNumber: cloneInt(series.RoundNumber), Format: series.Format, State: series.State, FirstDisplayName: series.FirstDisplayName, SecondDisplayName: series.SecondDisplayName, Score: PublicSeriesScore{FirstWins: series.FirstWins, SecondWins: series.SecondWins}, CurrentGamePosition: series.CurrentGamePosition, CurrentGame: currentGameFromInput(series.CurrentGame), ScheduledAt: cloneTime(series.ScheduledAt)}
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
			SeriesID:                input.Draft.SeriesID,
			Format:                  input.Draft.Format,
			State:                   input.Draft.State,
			FirstActorDisplayName:   input.Draft.FirstActorDisplayName,
			CurrentTurn:             cloneInt(input.Draft.CurrentTurn),
			CurrentAction:           cloneString(input.Draft.CurrentAction),
			CurrentActorDisplayName: cloneString(input.Draft.CurrentActorDisplayName),
			TurnDeadline:            cloneTime(input.Draft.TurnDeadline),
			AutoActionPending:       input.Draft.AutoActionPending,
			Pool:                    append([]string{}, input.Draft.Pool...),
			SelectedCategories:      append([]string{}, input.Draft.SelectedCategories...),
			Actions:                 make([]PublicDraftAction, len(input.Draft.Actions)),
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
	if s.Scoreboard == nil || s.SwissRounds == nil || s.Bracket == nil || s.LiveSeries == nil || s.OfficialResults == nil ||
		!validRealtimeCollections(len(s.Scoreboard), len(s.SwissRounds), len(s.Bracket), len(s.LiveSeries), len(s.OfficialResults)) {
		return fmt.Errorf("%w: public collections must be arrays", ErrInvalidPublicSnapshot)
	}
	for _, entry := range s.Scoreboard {
		if entry.Rank < 1 || !validRealtimeString(entry.DisplayName) || entry.Points < 0 || entry.Wins < 0 || entry.Losses < 0 ||
			entry.ByeCount < 0 || entry.Buchholz < 0 || entry.EffectiveTimeMS < 0 ||
			!validPublicQualificationStatus(entry.QualificationStatus) {
			return fmt.Errorf("%w: invalid scoreboard entry", ErrInvalidPublicSnapshot)
		}
	}
	previousRound := 0
	for _, round := range s.SwissRounds {
		if round.RoundNumber < 1 || round.RoundNumber > 4 || round.RoundNumber <= previousRound ||
			!domain.WaveState(round.State).IsValid() {
			return fmt.Errorf("%w: invalid Swiss round", ErrInvalidPublicSnapshot)
		}
		previousRound = round.RoundNumber
		if round.Bye != nil && (!validPublicDisplayName(round.Bye.DisplayName) || round.Bye.PointsAwarded != 1) {
			return fmt.Errorf("%w: invalid Swiss bye", ErrInvalidPublicSnapshot)
		}
	}
	if !validPublicBracket(s.Bracket) {
		return fmt.Errorf("%w: invalid bracket", ErrInvalidPublicSnapshot)
	}
	for _, series := range s.LiveSeries {
		if series.SeriesID == uuid.Nil || !validPublicStage(series.Stage) || (series.Stage == "swiss" && (series.RoundNumber == nil || *series.RoundNumber < 1 || *series.RoundNumber > 4)) || (series.Stage != "swiss" && series.RoundNumber != nil) || series.CurrentGamePosition < 0 || !validPublicLabels(series.Format, series.State, series.FirstDisplayName, series.SecondDisplayName) || !series.Score.valid() || !validOptionalUTC(series.ScheduledAt) {
			return fmt.Errorf("%w: invalid live series", ErrInvalidPublicSnapshot)
		}
		if series.CurrentGame != nil && series.CurrentGamePosition != series.CurrentGame.Position {
			return fmt.Errorf("%w: current game compatibility mismatch", ErrInvalidPublicSnapshot)
		}
		if series.CurrentGame != nil && !series.CurrentGame.valid() {
			return fmt.Errorf("%w: invalid current game", ErrInvalidPublicSnapshot)
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

func validPublicQualificationStatus(value string) bool {
	switch value {
	case "pending", "qualified", "eliminated":
		return true
	default:
		return false
	}
}

//nolint:gocyclo // Wire validation keeps the complete bracket-match invariant in one auditable predicate.
func validPublicBracketMatch(match PublicBracketMatch) bool {
	if match.Position < 1 || !validOptionalUTC(match.ScheduledAt) || !domain.SeriesState(match.State).IsValid() {
		return false
	}
	var maxWins int
	switch {
	case match.Stage == "semifinal" && match.Format == "bo1":
		maxWins = 1
	case match.Stage == "final" && match.Format == "bo3":
		maxWins = 2
	default:
		return false
	}
	if !match.Score.valid() || match.Score.FirstWins > maxWins || match.Score.SecondWins > maxWins {
		return false
	}
	if (match.FirstDisplayName == nil) != (match.SecondDisplayName == nil) {
		return false
	}
	if match.FirstDisplayName == nil {
		return match.Stage == "final" && match.State == "planned" && match.WinnerDisplayName == nil &&
			match.Score.FirstWins == 0 && match.Score.SecondWins == 0
	}
	if !validPublicDisplayName(*match.FirstDisplayName) || !validPublicDisplayName(*match.SecondDisplayName) {
		return false
	}
	if match.State != "completed" {
		return match.WinnerDisplayName == nil
	}
	if match.WinnerDisplayName == nil || !validPublicDisplayName(*match.WinnerDisplayName) ||
		match.Score.FirstWins == match.Score.SecondWins {
		return false
	}
	return (match.Score.FirstWins > match.Score.SecondWins && *match.WinnerDisplayName == *match.FirstDisplayName) ||
		(match.Score.SecondWins > match.Score.FirstWins && *match.WinnerDisplayName == *match.SecondDisplayName)
}

func validPublicBracket(matches []PublicBracketMatch) bool {
	if len(matches) == 0 {
		return true
	}
	if len(matches) != 3 {
		return false
	}
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if !validPublicBracketMatch(match) ||
			(match.Stage == "semifinal" && match.Position > 2) ||
			(match.Stage == "final" && match.Position != 1) {
			return false
		}
		key := fmt.Sprintf("%s:%d", match.Stage, match.Position)
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return len(seen) == 3
}

func (s PublicSnapshot) clone() PublicSnapshot {
	clone := s
	clone.Tournament.StartedAt = cloneTime(s.Tournament.StartedAt)
	clone.Tournament.FinishedAt = cloneTime(s.Tournament.FinishedAt)
	clone.Scoreboard = append([]PublicScoreboardEntry{}, s.Scoreboard...)
	clone.SwissRounds = append([]PublicSwissRound{}, s.SwissRounds...)
	for index, round := range clone.SwissRounds {
		if s.SwissRounds[index].Bye != nil {
			bye := *s.SwissRounds[index].Bye
			round.Bye = &bye
		} else {
			round.Bye = nil
		}
		clone.SwissRounds[index] = round
	}
	clone.Bracket = append([]PublicBracketMatch{}, s.Bracket...)
	clone.LiveSeries = append([]PublicSeries{}, s.LiveSeries...)
	for index := range clone.Bracket {
		clone.Bracket[index].FirstDisplayName = cloneString(s.Bracket[index].FirstDisplayName)
		clone.Bracket[index].SecondDisplayName = cloneString(s.Bracket[index].SecondDisplayName)
		clone.Bracket[index].WinnerDisplayName = cloneString(s.Bracket[index].WinnerDisplayName)
		clone.Bracket[index].ScheduledAt = cloneTime(s.Bracket[index].ScheduledAt)
	}
	for index := range clone.LiveSeries {
		clone.LiveSeries[index].RoundNumber = cloneInt(s.LiveSeries[index].RoundNumber)
		clone.LiveSeries[index].CurrentGame = cloneCurrentGame(s.LiveSeries[index].CurrentGame)
		clone.LiveSeries[index].ScheduledAt = cloneTime(s.LiveSeries[index].ScheduledAt)
	}
	clone.OfficialResults = append([]PublicOfficialResult{}, s.OfficialResults...)
	if s.Draft != nil {
		draft := *s.Draft
		draft.Pool = append([]string{}, s.Draft.Pool...)
		draft.SelectedCategories = append([]string{}, s.Draft.SelectedCategories...)
		draft.Actions = append([]PublicDraftAction{}, s.Draft.Actions...)
		draft.CurrentTurn = cloneInt(s.Draft.CurrentTurn)
		draft.CurrentAction = cloneString(s.Draft.CurrentAction)
		draft.CurrentActorDisplayName = cloneString(s.Draft.CurrentActorDisplayName)
		draft.TurnDeadline = cloneTime(s.Draft.TurnDeadline)
		clone.Draft = &draft
	}
	return clone
}

func (s PublicSeriesScore) valid() bool {
	return s.FirstWins >= 0 && s.SecondWins >= 0
}

func currentGameFromInput(value *PublicCurrentGameInput) *PublicCurrentGame {
	if value == nil {
		return nil
	}
	return &PublicCurrentGame{
		Position:               value.Position,
		Category:               value.Category,
		State:                  value.State,
		StartedAt:              cloneTime(value.StartedAt),
		EffectiveDeadline:      cloneTime(value.EffectiveDeadline),
		FinishedAt:             cloneTime(value.FinishedAt),
		SolveTimeMS:            cloneInt64(value.SolveTimeMS),
		ResultReason:           cloneString(value.ResultReason),
		WinnerDisplayName:      cloneString(value.WinnerDisplayName),
		FirstConnectionStatus:  value.FirstConnectionStatus,
		SecondConnectionStatus: value.SecondConnectionStatus,
	}
}

func cloneCurrentGame(value *PublicCurrentGame) *PublicCurrentGame {
	if value == nil {
		return nil
	}
	return &PublicCurrentGame{
		Position:               value.Position,
		Category:               value.Category,
		State:                  value.State,
		StartedAt:              cloneTime(value.StartedAt),
		EffectiveDeadline:      cloneTime(value.EffectiveDeadline),
		FinishedAt:             cloneTime(value.FinishedAt),
		SolveTimeMS:            cloneInt64(value.SolveTimeMS),
		ResultReason:           cloneString(value.ResultReason),
		WinnerDisplayName:      cloneString(value.WinnerDisplayName),
		FirstConnectionStatus:  value.FirstConnectionStatus,
		SecondConnectionStatus: value.SecondConnectionStatus,
	}
}

func (g PublicCurrentGame) valid() bool {
	state := domain.GameState(g.State)
	if !validPublicCurrentGameIdentity(g, state) || !validPublicCurrentGameLifecycle(g, state) {
		return false
	}
	return validPublicCurrentGameResult(g, state)
}

func validPublicCurrentGameIdentity(g PublicCurrentGame, state domain.GameState) bool {
	return g.Position >= 1 && g.Position <= 3 && domain.Category(g.Category).IsValid() && state.IsValid() &&
		validPublicConnectionStatus(g.FirstConnectionStatus) && validPublicConnectionStatus(g.SecondConnectionStatus) &&
		validOptionalUTC(g.StartedAt) && validOptionalUTC(g.EffectiveDeadline) && validOptionalUTC(g.FinishedAt) &&
		(g.SolveTimeMS == nil || (*g.SolveTimeMS >= 0 && *g.SolveTimeMS <= usecase.MaxPublicSolveTimeMS))
}

func validPublicCurrentGameLifecycle(g PublicCurrentGame, state domain.GameState) bool {
	if !state.IsTerminal() && g.FinishedAt != nil {
		return false
	}
	if state.IsTerminal() && g.FinishedAt == nil {
		return false
	}
	if (state == domain.GameStateActive || state == domain.GameStatePaused) && g.StartedAt == nil {
		return false
	}
	if state == domain.GameStateActive && g.EffectiveDeadline == nil {
		return false
	}
	if state == domain.GameStatePaused && g.EffectiveDeadline != nil {
		return false
	}
	return !state.IsTerminal() || g.EffectiveDeadline == nil
}

func validPublicCurrentGameResult(g PublicCurrentGame, state domain.GameState) bool {
	if !state.IsTerminal() {
		return g.ResultReason == nil && g.WinnerDisplayName == nil && g.SolveTimeMS == nil
	}
	if g.ResultReason == nil || !domain.GameResultReason(*g.ResultReason).IsLegalFor(state) {
		return false
	}
	if state == domain.GameStateCompleted {
		if g.WinnerDisplayName == nil || !validPublicDisplayName(*g.WinnerDisplayName) {
			return false
		}
		if *g.ResultReason != string(domain.GameResultReasonSolved) && g.SolveTimeMS != nil {
			return false
		}
		return true
	}
	return g.WinnerDisplayName == nil && g.SolveTimeMS == nil
}

func (d PublicDraft) validate() error {
	if !validPublicDraftHeader(d) {
		return fmt.Errorf("%w: invalid draft view", ErrInvalidPublicSnapshot)
	}
	poolSize, actionCount, selectedCount, ok := publicDraftCardinality(d.Format)
	if !ok {
		return fmt.Errorf("%w: invalid draft format", ErrInvalidPublicSnapshot)
	}
	if len(d.Pool) != poolSize || len(d.SelectedCategories) > selectedCount || len(d.Actions) > actionCount {
		return fmt.Errorf("%w: invalid draft cardinality", ErrInvalidPublicSnapshot)
	}
	if err := validatePublicDraftCategories(d); err != nil {
		return err
	}
	if err := validatePublicDraftActions(d); err != nil {
		return err
	}
	if err := validatePublicDraftStateFields(d, actionCount, selectedCount); err != nil {
		return err
	}
	if !validOptionalUTC(d.TurnDeadline) {
		return fmt.Errorf("%w: invalid draft deadline", ErrInvalidPublicSnapshot)
	}
	return nil
}

func validPublicDraftHeader(d PublicDraft) bool {
	return d.SeriesID != uuid.Nil && validPublicLabels(d.Format, d.State, d.FirstActorDisplayName) &&
		validPublicDraftState(d.State) && d.Pool != nil && d.SelectedCategories != nil && d.Actions != nil &&
		validRealtimeCollections(len(d.Pool), len(d.SelectedCategories), len(d.Actions))
}

func publicDraftCardinality(format string) (poolSize, actionCount, selectedCount int, ok bool) {
	switch format {
	case "bo1":
		return 3, 2, 1, true
	case "bo3":
		return 5, 4, 3, true
	default:
		return 0, 0, 0, false
	}
}

func validatePublicDraftCategories(d PublicDraft) error {
	for _, value := range append(append([]string{}, d.Pool...), d.SelectedCategories...) {
		if !validRealtimeString(value) || !domain.Category(value).IsValid() {
			return fmt.Errorf("%w: empty draft category", ErrInvalidPublicSnapshot)
		}
	}
	seenPool := make(map[string]struct{}, len(d.Pool))
	for _, value := range d.Pool {
		if _, exists := seenPool[value]; exists {
			return fmt.Errorf("%w: duplicate draft pool category", ErrInvalidPublicSnapshot)
		}
		seenPool[value] = struct{}{}
	}
	seenSelected := make(map[string]struct{}, len(d.SelectedCategories))
	for _, value := range d.SelectedCategories {
		if _, exists := seenPool[value]; !exists {
			return fmt.Errorf("%w: selected category outside pool", ErrInvalidPublicSnapshot)
		}
		if _, exists := seenSelected[value]; exists {
			return fmt.Errorf("%w: duplicate selected category", ErrInvalidPublicSnapshot)
		}
		seenSelected[value] = struct{}{}
	}
	return nil
}

func validatePublicDraftActions(d PublicDraft) error {
	for index, action := range d.Actions {
		if action.Turn != index+1 || action.Turn < 1 || action.Turn > 4 ||
			!domain.DraftActionType(action.Action).IsValid() || !validPublicLabels(action.Category, action.ActorDisplayName) ||
			!domain.IsValidServerTime(action.OccurredAt) {
			return fmt.Errorf("%w: invalid draft action", ErrInvalidPublicSnapshot)
		}
	}
	return nil
}

func validatePublicDraftStateFields(d PublicDraft, actionCount, selectedCount int) error {
	switch d.State {
	case "completed":
		return validateCompletedPublicDraft(d, actionCount, selectedCount)
	case "superseded":
		return validateSupersededPublicDraft(d)
	case "active", "paused", "recovery_required":
		return validateActivePublicDraft(d, actionCount)
	}
	return nil
}

func validateCompletedPublicDraft(d PublicDraft, actionCount, selectedCount int) error {
	if len(d.Actions) != actionCount || len(d.SelectedCategories) != selectedCount ||
		d.CurrentTurn != nil || d.CurrentAction != nil || d.CurrentActorDisplayName != nil ||
		d.TurnDeadline != nil || d.AutoActionPending {
		return fmt.Errorf("%w: invalid completed draft", ErrInvalidPublicSnapshot)
	}
	return nil
}

func validateSupersededPublicDraft(d PublicDraft) error {
	if d.CurrentTurn != nil || d.CurrentAction != nil || d.CurrentActorDisplayName != nil ||
		d.TurnDeadline != nil || d.AutoActionPending {
		return fmt.Errorf("%w: invalid superseded draft", ErrInvalidPublicSnapshot)
	}
	return nil
}

func validateActivePublicDraft(d PublicDraft, actionCount int) error {
	if len(d.SelectedCategories) != 0 || d.CurrentTurn == nil || *d.CurrentTurn != len(d.Actions)+1 ||
		*d.CurrentTurn > actionCount || d.CurrentAction == nil ||
		!domain.DraftActionType(*d.CurrentAction).IsValid() || d.CurrentActorDisplayName == nil ||
		!validPublicDisplayName(*d.CurrentActorDisplayName) {
		return fmt.Errorf("%w: invalid current draft turn", ErrInvalidPublicSnapshot)
	}
	if d.State == "active" && d.TurnDeadline == nil {
		return fmt.Errorf("%w: active draft deadline missing", ErrInvalidPublicSnapshot)
	}
	if d.State != "active" && d.TurnDeadline != nil {
		return fmt.Errorf("%w: paused draft deadline present", ErrInvalidPublicSnapshot)
	}
	return nil
}

func validPublicDraftState(value string) bool {
	switch value {
	case "active", "paused", "recovery_required", "completed", "superseded":
		return true
	default:
		return false
	}
}

func validPublicConnectionStatus(value string) bool {
	return value == "connected" || value == "disconnected" || value == "unknown"
}

func validPublicLabels(values ...string) bool {
	for _, value := range values {
		if !validRealtimeString(value) {
			return false
		}
	}
	return true
}

func validPublicDisplayName(value string) bool {
	return validRealtimeString(value) && utf8.RuneCountInString(value) <= 64
}

func validPublicStage(value string) bool {
	switch value {
	case "swiss", "golden", "semifinal", "final":
		return true
	default:
		return false
	}
}
