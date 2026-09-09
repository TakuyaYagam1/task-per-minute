package domain_test

import (
	"errors"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestSeries(t *testing.T) {
	t.Parallel()

	seriesID := uuid.New()
	firstID := uuid.New()
	secondID := uuid.New()
	winnerID := firstID
	scoreRevisionID := domain.SeriesScoreRevisionID(uuid.New())
	resultRevisionID := domain.OfficialResultRevisionID(uuid.New())
	gameResultRevisionID := domain.OfficialResultRevisionID(uuid.New())
	slotID := uuid.New()
	series := domain.Series{
		ID:                      seriesID,
		TournamentID:            uuid.New(),
		FirstParticipantID:      firstID,
		SecondParticipantID:     secondID,
		Format:                  domain.SeriesFormatBO1,
		State:                   domain.SeriesStateCompleted,
		Score:                   domain.SeriesScore{FirstParticipantWins: 1},
		WinnerID:                &winnerID,
		CurrentScoreRevisionID:  &scoreRevisionID,
		CurrentResultRevisionID: &resultRevisionID,
		Slots: []domain.GameSlot{{
			ID:          slotID,
			SeriesID:    seriesID,
			Position:    1,
			Category:    domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID:               uuid.New(),
				SlotID:           slotID,
				AttemptNo:        1,
				State:            domain.GameStateCompleted,
				ResultReason:     domain.GameResultReasonSolved,
				WinnerID:         &winnerID,
				ResultRevisionID: &gameResultRevisionID,
			}},
		}},
	}

	if err := series.Validate(); err != nil {
		t.Fatalf("valid series rejected: %v", err)
	}
	if got := scoreRevisionID.UUID(); got == uuid.Nil {
		t.Fatal("score revision identity is empty")
	}
	if got := resultRevisionID.UUID(); got == uuid.Nil {
		t.Fatal("result revision identity is empty")
	}
}

func TestSeriesRejectsInvalidTerminalEvidence(t *testing.T) {
	t.Parallel()

	firstID := uuid.New()
	secondID := uuid.New()
	winnerID := firstID
	scoreRevisionID := domain.SeriesScoreRevisionID(uuid.New())
	resultRevisionID := domain.OfficialResultRevisionID(uuid.New())
	valid := domain.Series{
		ID:                      uuid.New(),
		TournamentID:            uuid.New(),
		FirstParticipantID:      firstID,
		SecondParticipantID:     secondID,
		Format:                  domain.SeriesFormatBO1,
		State:                   domain.SeriesStateCompleted,
		Score:                   domain.SeriesScore{FirstParticipantWins: 1},
		WinnerID:                &winnerID,
		CurrentScoreRevisionID:  &scoreRevisionID,
		CurrentResultRevisionID: &resultRevisionID,
	}

	tests := []struct {
		name   string
		mutate func(*domain.Series)
	}{
		{name: "missing winner", mutate: func(s *domain.Series) { s.WinnerID = nil }},
		{name: "missing result revision", mutate: func(s *domain.Series) { s.CurrentResultRevisionID = nil }},
		{name: "score has no winner", mutate: func(s *domain.Series) { s.Score = domain.SeriesScore{} }},
		{name: "duplicate participant", mutate: func(s *domain.Series) { s.SecondParticipantID = s.FirstParticipantID }},
		{name: "non-terminal result revision", mutate: func(s *domain.Series) { s.State = domain.SeriesStateActive; s.WinnerID = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			series := valid
			tt.mutate(&series)
			if err := series.Validate(); !errors.Is(err, domain.ErrInvalidSeries) {
				t.Fatalf("Validate() error = %v, want ErrInvalidTournamentSeries", err)
			}
		})
	}
}

func TestGameTerminalReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state  domain.GameState
		reason domain.GameResultReason
		legal  bool
	}{
		{domain.GameStateCompleted, domain.GameResultReasonSolved, true},
		{domain.GameStateCompleted, domain.GameResultReasonSurrender, true},
		{domain.GameStateCompleted, domain.GameResultReasonOperatorForfeit, true},
		{domain.GameStateVoid, domain.GameResultReasonNoSolve, true},
		{domain.GameStateVoid, domain.GameResultReasonTaskFailure, true},
		{domain.GameStateVoid, domain.GameResultReasonCommonPlatformFailure, true},
		{domain.GameStateVoid, domain.GameResultReasonDisconnect, true},
		{domain.GameStateVoid, domain.GameResultReasonExecutionEpochBreak, true},
		{domain.GameStateCancelled, domain.GameResultReasonNoShow, true},
		{domain.GameStateCancelled, domain.GameResultReasonSeriesCancelled, true},
		{domain.GameStateCancelled, domain.GameResultReasonTournamentCancelled, true},
		{domain.GameStateSuperseded, domain.GameResultReasonDerivedRevisionSuperseded, true},
		{domain.GameStateCompleted, domain.GameResultReasonNoSolve, false},
		{domain.GameStateVoid, domain.GameResultReasonSolved, false},
		{domain.GameStateActive, domain.GameResultReasonSolved, false},
	}

	for _, tt := range tests {
		if got := tt.reason.IsLegalFor(tt.state); got != tt.legal {
			t.Errorf("%s.IsLegalFor(%s) = %v, want %v", tt.reason, tt.state, got, tt.legal)
		}
	}
}

func TestGameSlotOrdersAttempts(t *testing.T) {
	t.Parallel()

	slotID := uuid.New()
	voidRevisionID := domain.OfficialResultRevisionID(uuid.New())
	active := domain.Game{
		ID:        uuid.New(),
		SlotID:    slotID,
		AttemptNo: 2,
		State:     domain.GameStateActive,
	}
	slot := domain.GameSlot{
		ID:          slotID,
		SeriesID:    uuid.New(),
		Position:    1,
		Category:    domain.CategoryCrypto,
		ScoreBefore: domain.SeriesScore{},
		Attempts: []domain.Game{
			{
				ID:               uuid.New(),
				SlotID:           slotID,
				AttemptNo:        1,
				State:            domain.GameStateVoid,
				ResultReason:     domain.GameResultReasonNoSolve,
				ResultRevisionID: &voidRevisionID,
			},
			active,
		},
	}

	if err := slot.Validate(); err != nil {
		t.Fatalf("valid replay chain rejected: %v", err)
	}
	if slot.AttemptCount() != 2 || slot.NextAttemptNo() != 3 {
		t.Fatalf("attempt counters = %d/%d, want 2/3", slot.AttemptCount(), slot.NextAttemptNo())
	}

	wrongOrder := slot
	wrongOrder.Attempts = append([]domain.Game(nil), slot.Attempts...)
	wrongOrder.Attempts[1].AttemptNo = 3
	if err := wrongOrder.Validate(); !errors.Is(err, domain.ErrInvalidGameSlot) {
		t.Fatalf("wrong attempt order error = %v", err)
	}
}

func TestGameSupersededCannotReactivate(t *testing.T) {
	t.Parallel()

	slotID := uuid.New()
	resultRevisionID := domain.OfficialResultRevisionID(uuid.New())
	slot := domain.GameSlot{
		ID:          slotID,
		SeriesID:    uuid.New(),
		Position:    1,
		Category:    domain.CategoryReverse,
		ScoreBefore: domain.SeriesScore{},
		Attempts: []domain.Game{
			{
				ID:               uuid.New(),
				SlotID:           slotID,
				AttemptNo:        1,
				State:            domain.GameStateSuperseded,
				ResultReason:     domain.GameResultReasonDerivedRevisionSuperseded,
				ResultRevisionID: &resultRevisionID,
			},
			{
				ID:        uuid.New(),
				SlotID:    slotID,
				AttemptNo: 2,
				State:     domain.GameStateActive,
			},
		},
	}

	if err := slot.Validate(); !errors.Is(err, domain.ErrInvalidGameSlot) {
		t.Fatalf("superseded reactivation error = %v, want ErrInvalidTournamentGameSlot", err)
	}
}
