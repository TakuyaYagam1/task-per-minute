package arena_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSwissNormalOrdering(t *testing.T) {
	t.Parallel()

	t.Run("orders exact two-player ties and keeps accepted time informational", func(t *testing.T) {
		t.Parallel()

		participants := task027ParticipantIDs(4)
		accepted := 20 * time.Second
		ledgerInput := arena.SwissPointLedgerInput{
			ParticipantIDs: participants,
			Series: []arena.SwissSeriesPointResult{
				task027SeriesResult(1, 1, participants[0], participants[1], participants[0], arena.SwissSeriesResultPlayed, 3*time.Minute, time.Minute),
				task027SeriesResult(2, 2, participants[1], participants[2], participants[1], arena.SwissSeriesResultPlayed, time.Minute, 2*time.Minute),
				task027SeriesResult(3, 3, participants[0], participants[3], participants[3], arena.SwissSeriesResultPlayed, 0, 2*time.Minute),
			},
			Byes: []arena.SwissByePointResult{{
				RoundID: task027ID(104), RoundNumber: 4, ParticipantID: participants[2], RevisionID: task027ID(404),
			}},
		}
		ledgerInput.Series[0].FirstAcceptedSolveTime = &accepted
		ledger := mustBuildSwissPointLedger(t, ledgerInput)

		ordered, err := arena.OrderSwissNormalStandings(arena.SwissNormalOrderingInput{
			Ledger: ledger,
			Seeds: []arena.SwissParticipantSeed{
				{ParticipantID: participants[0], Seed: 4},
				{ParticipantID: participants[1], Seed: 3},
				{ParticipantID: participants[2], Seed: 2},
				{ParticipantID: participants[3], Seed: 1},
			},
			SwissComplete: false,
		})
		if err != nil {
			t.Fatalf("OrderSwissNormalStandings() error = %v", err)
		}
		wantOrder := []uuid.UUID{participants[0], participants[1], participants[3], participants[2]}
		for index, wantID := range wantOrder {
			if ordered[index].ParticipantID != wantID || ordered[index].Position != index+1 {
				t.Fatalf("position %d = %+v, want participant %s", index+1, ordered[index], wantID)
			}
			if ordered[index].PointsLabel != arena.SwissPointsProvisional {
				t.Errorf("participant %s label = %q, want provisional", wantID, ordered[index].PointsLabel)
			}
		}
		if ordered[0].Buchholz != 2 || ordered[1].Buchholz != 2 {
			t.Fatalf("top Buchholz = %d, %d, want 2, 2", ordered[0].Buchholz, ordered[1].Buchholz)
		}
		if !ordered[0].HeadToHeadApplied || ordered[0].HeadToHeadPoints != 1 ||
			!ordered[1].HeadToHeadApplied || ordered[1].HeadToHeadPoints != 0 {
			t.Fatalf("top head-to-head = %+v, %+v", ordered[0], ordered[1])
		}
		if ordered[0].EffectiveTime <= ordered[1].EffectiveTime {
			t.Fatalf("head-to-head did not outrank effective time: %s <= %s", ordered[0].EffectiveTime, ordered[1].EffectiveTime)
		}
		if ordered[0].AcceptedSolveTime == nil || *ordered[0].AcceptedSolveTime != accepted {
			t.Fatalf("accepted solve time = %v, want %s", ordered[0].AcceptedSolveTime, accepted)
		}

		finalInput := arena.SwissNormalOrderingInput{
			Ledger: ledger,
			Seeds: []arena.SwissParticipantSeed{
				{ParticipantID: participants[0], Seed: 4},
				{ParticipantID: participants[1], Seed: 3},
				{ParticipantID: participants[2], Seed: 2},
				{ParticipantID: participants[3], Seed: 1},
			},
			SwissComplete: true,
		}
		final, err := arena.OrderSwissNormalStandings(finalInput)
		if err != nil || final[0].PointsLabel != arena.SwissPointsFinal {
			t.Fatalf("final standings error = %v, label = %q", err, final[0].PointsLabel)
		}
	})

	t.Run("keeps bye void and no-show semantics distinct", func(t *testing.T) {
		t.Parallel()

		participants := task027ParticipantIDs(5)
		ledger := mustBuildSwissPointLedger(t, arena.SwissPointLedgerInput{
			ParticipantIDs: participants,
			Series: []arena.SwissSeriesPointResult{
				task027SeriesResult(10, 1, participants[0], participants[1], participants[0], arena.SwissSeriesResultNoShow, 0, 3*time.Minute),
				task027SeriesResult(11, 1, participants[2], participants[3], uuid.Nil, arena.SwissSeriesResultVoid, 0, 0),
			},
			Byes: []arena.SwissByePointResult{{
				RoundID: task027ID(101), RoundNumber: 1, ParticipantID: participants[4], RevisionID: task027ID(411),
			}},
		})
		ordered, err := arena.OrderSwissNormalStandings(arena.SwissNormalOrderingInput{
			Ledger: ledger,
			Seeds: []arena.SwissParticipantSeed{
				{ParticipantID: participants[0], Seed: 1},
				{ParticipantID: participants[1], Seed: 5},
				{ParticipantID: participants[2], Seed: 4},
				{ParticipantID: participants[3], Seed: 3},
				{ParticipantID: participants[4], Seed: 2},
			},
		})
		if err != nil {
			t.Fatalf("OrderSwissNormalStandings() error = %v", err)
		}
		if ordered[0].ParticipantID != participants[0] || ordered[1].ParticipantID != participants[4] {
			t.Fatalf("top order = %s, %s, want no-show winner then bye", ordered[0].ParticipantID, ordered[1].ParticipantID)
		}
		if ledger.Entries[0].Label != arena.SwissSeriesResultNoShow ||
			ledger.Entries[1].Label != arena.SwissSeriesResultVoid ||
			ledger.Entries[2].SourceKind != arena.SwissPointSourceBye {
			t.Fatalf("operator labels = %+v", ledger.Entries)
		}
	})

	t.Run("rejects tampered ledger evidence", func(t *testing.T) {
		t.Parallel()

		participants := task027ParticipantIDs(4)
		ledger := mustBuildSwissPointLedger(t, arena.SwissPointLedgerInput{
			ParticipantIDs: participants,
			Series: []arena.SwissSeriesPointResult{
				task027SeriesResult(30, 1, participants[0], participants[1], participants[0], arena.SwissSeriesResultPlayed, time.Minute, 2*time.Minute),
			},
		})
		ledger.Entries[0].Label = arena.SwissSeriesResultLabel("edited")
		_, err := arena.OrderSwissNormalStandings(arena.SwissNormalOrderingInput{
			Ledger: ledger,
			Seeds: []arena.SwissParticipantSeed{
				{ParticipantID: participants[0], Seed: 1},
				{ParticipantID: participants[1], Seed: 2},
				{ParticipantID: participants[2], Seed: 3},
				{ParticipantID: participants[3], Seed: 4},
			},
		})
		if !errors.Is(err, arena.ErrInvalidSwissPointLedger) {
			t.Fatalf("OrderSwissNormalStandings(tampered) error = %v, want ErrInvalidSwissPointLedger", err)
		}
	})
}

func task027SeriesResult(
	number int,
	round int,
	first uuid.UUID,
	second uuid.UUID,
	winner uuid.UUID,
	label arena.SwissSeriesResultLabel,
	firstTime time.Duration,
	secondTime time.Duration,
) arena.SwissSeriesPointResult {
	var winnerID *uuid.UUID
	if winner != uuid.Nil {
		winnerID = task027IDPointer(winner)
	}
	return arena.SwissSeriesPointResult{
		RoundID: task027ID(100 + round), RoundNumber: round, SeriesID: task027ID(200 + number),
		ResultRevisionID:   domain.ArenaOfficialResultRevisionID(task027ID(300 + number)),
		FirstParticipantID: first, SecondParticipantID: second, WinnerID: winnerID, Label: label,
		FirstEffectiveTime: firstTime, SecondEffectiveTime: secondTime,
	}
}

func mustBuildSwissPointLedger(t *testing.T, input arena.SwissPointLedgerInput) arena.SwissPointLedger {
	t.Helper()
	ledger, err := arena.BuildSwissPointLedger(input)
	if err != nil {
		t.Fatalf("BuildSwissPointLedger() error = %v", err)
	}
	return ledger
}
