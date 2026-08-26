package domain_test

import (
	"errors"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestArenaSeries(t *testing.T) {
	t.Parallel()

	seriesID := uuid.New()
	firstID := uuid.New()
	secondID := uuid.New()
	winnerID := firstID
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(uuid.New())
	resultRevisionID := domain.ArenaOfficialResultRevisionID(uuid.New())
	gameResultRevisionID := domain.ArenaOfficialResultRevisionID(uuid.New())
	slotID := uuid.New()
	series := domain.ArenaSeries{
		ID:                      seriesID,
		TournamentID:            uuid.New(),
		FirstParticipantID:      firstID,
		SecondParticipantID:     secondID,
		Format:                  domain.ArenaSeriesFormatBO1,
		State:                   domain.ArenaSeriesStateCompleted,
		Score:                   domain.ArenaSeriesScore{FirstParticipantWins: 1},
		WinnerID:                &winnerID,
		CurrentScoreRevisionID:  &scoreRevisionID,
		CurrentResultRevisionID: &resultRevisionID,
		Slots: []domain.ArenaGameSlot{{
			ID:          slotID,
			SeriesID:    seriesID,
			Position:    1,
			Category:    domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID:               uuid.New(),
				SlotID:           slotID,
				AttemptNo:        1,
				State:            domain.ArenaGameStateCompleted,
				ResultReason:     domain.ArenaGameResultReasonSolved,
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

func TestArenaSeriesRejectsInvalidTerminalEvidence(t *testing.T) {
	t.Parallel()

	firstID := uuid.New()
	secondID := uuid.New()
	winnerID := firstID
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(uuid.New())
	resultRevisionID := domain.ArenaOfficialResultRevisionID(uuid.New())
	valid := domain.ArenaSeries{
		ID:                      uuid.New(),
		TournamentID:            uuid.New(),
		FirstParticipantID:      firstID,
		SecondParticipantID:     secondID,
		Format:                  domain.ArenaSeriesFormatBO1,
		State:                   domain.ArenaSeriesStateCompleted,
		Score:                   domain.ArenaSeriesScore{FirstParticipantWins: 1},
		WinnerID:                &winnerID,
		CurrentScoreRevisionID:  &scoreRevisionID,
		CurrentResultRevisionID: &resultRevisionID,
	}

	tests := []struct {
		name   string
		mutate func(*domain.ArenaSeries)
	}{
		{name: "missing winner", mutate: func(s *domain.ArenaSeries) { s.WinnerID = nil }},
		{name: "missing result revision", mutate: func(s *domain.ArenaSeries) { s.CurrentResultRevisionID = nil }},
		{name: "score has no winner", mutate: func(s *domain.ArenaSeries) { s.Score = domain.ArenaSeriesScore{} }},
		{name: "duplicate participant", mutate: func(s *domain.ArenaSeries) { s.SecondParticipantID = s.FirstParticipantID }},
		{name: "non-terminal result revision", mutate: func(s *domain.ArenaSeries) { s.State = domain.ArenaSeriesStateActive; s.WinnerID = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			series := valid
			tt.mutate(&series)
			if err := series.Validate(); !errors.Is(err, domain.ErrInvalidArenaSeries) {
				t.Fatalf("Validate() error = %v, want ErrInvalidArenaSeries", err)
			}
		})
	}
}

func TestArenaGameTerminalReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state  domain.ArenaGameState
		reason domain.ArenaGameResultReason
		legal  bool
	}{
		{domain.ArenaGameStateCompleted, domain.ArenaGameResultReasonSolved, true},
		{domain.ArenaGameStateCompleted, domain.ArenaGameResultReasonSurrender, true},
		{domain.ArenaGameStateCompleted, domain.ArenaGameResultReasonOperatorForfeit, true},
		{domain.ArenaGameStateVoid, domain.ArenaGameResultReasonNoSolve, true},
		{domain.ArenaGameStateVoid, domain.ArenaGameResultReasonTaskFailure, true},
		{domain.ArenaGameStateVoid, domain.ArenaGameResultReasonCommonPlatformFailure, true},
		{domain.ArenaGameStateVoid, domain.ArenaGameResultReasonDisconnect, true},
		{domain.ArenaGameStateVoid, domain.ArenaGameResultReasonExecutionEpochBreak, true},
		{domain.ArenaGameStateCancelled, domain.ArenaGameResultReasonNoShow, true},
		{domain.ArenaGameStateCancelled, domain.ArenaGameResultReasonSeriesCancelled, true},
		{domain.ArenaGameStateCancelled, domain.ArenaGameResultReasonTournamentCancelled, true},
		{domain.ArenaGameStateSuperseded, domain.ArenaGameResultReasonDerivedRevisionSuperseded, true},
		{domain.ArenaGameStateCompleted, domain.ArenaGameResultReasonNoSolve, false},
		{domain.ArenaGameStateVoid, domain.ArenaGameResultReasonSolved, false},
		{domain.ArenaGameStateActive, domain.ArenaGameResultReasonSolved, false},
	}

	for _, tt := range tests {
		if got := tt.reason.IsLegalFor(tt.state); got != tt.legal {
			t.Errorf("%s.IsLegalFor(%s) = %v, want %v", tt.reason, tt.state, got, tt.legal)
		}
	}
}

func TestArenaGameSlotOrdersAttempts(t *testing.T) {
	t.Parallel()

	slotID := uuid.New()
	voidRevisionID := domain.ArenaOfficialResultRevisionID(uuid.New())
	active := domain.ArenaGame{
		ID:        uuid.New(),
		SlotID:    slotID,
		AttemptNo: 2,
		State:     domain.ArenaGameStateActive,
	}
	slot := domain.ArenaGameSlot{
		ID:          slotID,
		SeriesID:    uuid.New(),
		Position:    1,
		Category:    domain.CategoryCrypto,
		ScoreBefore: domain.ArenaSeriesScore{},
		Attempts: []domain.ArenaGame{
			{
				ID:               uuid.New(),
				SlotID:           slotID,
				AttemptNo:        1,
				State:            domain.ArenaGameStateVoid,
				ResultReason:     domain.ArenaGameResultReasonNoSolve,
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
	wrongOrder.Attempts = append([]domain.ArenaGame(nil), slot.Attempts...)
	wrongOrder.Attempts[1].AttemptNo = 3
	if err := wrongOrder.Validate(); !errors.Is(err, domain.ErrInvalidArenaGameSlot) {
		t.Fatalf("wrong attempt order error = %v", err)
	}
}

func TestArenaGameSupersededCannotReactivate(t *testing.T) {
	t.Parallel()

	slotID := uuid.New()
	resultRevisionID := domain.ArenaOfficialResultRevisionID(uuid.New())
	slot := domain.ArenaGameSlot{
		ID:          slotID,
		SeriesID:    uuid.New(),
		Position:    1,
		Category:    domain.CategoryReverse,
		ScoreBefore: domain.ArenaSeriesScore{},
		Attempts: []domain.ArenaGame{
			{
				ID:               uuid.New(),
				SlotID:           slotID,
				AttemptNo:        1,
				State:            domain.ArenaGameStateSuperseded,
				ResultReason:     domain.ArenaGameResultReasonDerivedRevisionSuperseded,
				ResultRevisionID: &resultRevisionID,
			},
			{
				ID:        uuid.New(),
				SlotID:    slotID,
				AttemptNo: 2,
				State:     domain.ArenaGameStateActive,
			},
		},
	}

	if err := slot.Validate(); !errors.Is(err, domain.ErrInvalidArenaGameSlot) {
		t.Fatalf("superseded reactivation error = %v, want ErrInvalidArenaGameSlot", err)
	}
}
