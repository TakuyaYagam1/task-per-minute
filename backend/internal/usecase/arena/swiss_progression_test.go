package arena_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSwissRoundProgression(t *testing.T) {
	t.Parallel()

	generatedAt := time.Date(2026, time.August, 29, 14, 15, 0, 0, time.UTC)

	t.Run("generates one complete editable first round", func(t *testing.T) {
		t.Parallel()

		command := task027ProgressionCommand(nil, 1, 0, generatedAt)
		plan, err := arena.PrepareSwissRoundProgression(command)
		if err != nil {
			t.Fatalf("PrepareSwissRoundProgression() error = %v", err)
		}
		if err := plan.Validate(); err != nil {
			t.Fatalf("plan Validate() error = %v", err)
		}
		if plan.RoundNumber != 1 || len(plan.Pairing.Pairings) != 2 || plan.Bye == nil {
			t.Fatalf("first-round plan = %+v", plan)
		}
		assertProgressionCoverage(t, plan)

		repository := newSwissProgressionRepositoryFake(plan)
		useCase := arena.NewSwissRoundProgressionUseCase(repository)
		draft, changed, err := useCase.Apply(t.Context(), plan)
		if err != nil || !changed {
			t.Fatalf("Apply() error = %v, changed = %v", err, changed)
		}
		if draft.Revision != 1 || draft.LockedAt != nil || draft.StartedAt != nil {
			t.Fatalf("created draft = %+v", draft)
		}

		retried, repeated, err := useCase.Apply(t.Context(), plan)
		if err != nil || repeated || !reflect.DeepEqual(retried, draft) {
			t.Fatalf("Apply(retry) error = %v, changed = %v, draft = %+v", err, repeated, retried)
		}
	})

	t.Run("blocks incomplete official history", func(t *testing.T) {
		t.Parallel()

		participants := task027ParticipantIDs(5)
		round := task027OfficialRound(participants, 1)
		round.Series = round.Series[:1]
		command := task027ProgressionCommand([]arena.SwissProgressionRound{round}, 2, 0, generatedAt)
		if _, err := arena.PrepareSwissRoundProgression(command); !errors.Is(err, arena.ErrSwissRoundIncomplete) {
			t.Fatalf("PrepareSwissRoundProgression(incomplete) error = %v, want ErrSwissRoundIncomplete", err)
		}

		unofficial := task027OfficialRound(participants, 1)
		unofficial.Series[0].ResultRevisionID = domain.ArenaOfficialResultRevisionID{}
		command.History = []arena.SwissProgressionRound{unofficial}
		if _, err := arena.PrepareSwissRoundProgression(command); !errors.Is(err, arena.ErrSwissRoundIncomplete) {
			t.Fatalf("PrepareSwissRoundProgression(unofficial) error = %v, want ErrSwissRoundIncomplete", err)
		}
	})

	t.Run("rejects reuse of a prior round identity", func(t *testing.T) {
		t.Parallel()

		participants := task027ParticipantIDs(5)
		history := []arena.SwissProgressionRound{task027OfficialRound(participants, 1)}
		command := task027ProgressionCommand(history, 2, 0, generatedAt)
		command.RoundID = history[0].RoundID
		if _, err := arena.PrepareSwissRoundProgression(command); !errors.Is(err, arena.ErrInvalidSwissRoundProgression) {
			t.Fatalf("PrepareSwissRoundProgression(reused round ID) error = %v", err)
		}
	})

	t.Run("replaces only an unlocked draft from corrected official history", func(t *testing.T) {
		t.Parallel()

		participants := task027ParticipantIDs(5)
		history := []arena.SwissProgressionRound{task027OfficialRound(participants, 1)}
		firstCommand := task027ProgressionCommand(history, 2, 0, generatedAt)
		firstPlan, err := arena.PrepareSwissRoundProgression(firstCommand)
		if err != nil {
			t.Fatalf("PrepareSwissRoundProgression(first) error = %v", err)
		}
		repository := newSwissProgressionRepositoryFake(firstPlan)
		useCase := arena.NewSwissRoundProgressionUseCase(repository)
		first, changed, err := useCase.Apply(t.Context(), firstPlan)
		if err != nil || !changed {
			t.Fatalf("Apply(first) error = %v, changed = %v", err, changed)
		}

		correctedHistory := cloneTask027ProgressionHistory(history)
		oldRevision := correctedHistory[0].Series[0].ResultRevisionID.UUID()
		correctedHistory[0].Series[0].ResultRevisionID = domain.ArenaOfficialResultRevisionID(task027ID(390))
		correctedHistory[0].Series[0].WinnerID = task027IDPointer(participants[1])
		correctedCommand := task027ProgressionCommand(correctedHistory, 3, first.Revision, generatedAt.Add(time.Minute))
		correctedCommand.RoundID = firstPlan.RoundID
		correctedPlan, err := arena.PrepareSwissRoundProgression(correctedCommand)
		if err != nil {
			t.Fatalf("PrepareSwissRoundProgression(corrected) error = %v", err)
		}
		if correctedPlan.ProofHash == firstPlan.ProofHash {
			t.Fatal("corrected plan retained the old proof hash")
		}
		if slices.Contains(correctedPlan.SourceResultRevisionIDs, oldRevision) ||
			!slices.Contains(correctedPlan.SourceResultRevisionIDs, task027ID(390)) {
			t.Fatalf("corrected result revisions = %v", correctedPlan.SourceResultRevisionIDs)
		}

		repository.setAuthority(correctedPlan)
		corrected, updated, err := useCase.Apply(t.Context(), correctedPlan)
		if err != nil || !updated {
			t.Fatalf("Apply(corrected) error = %v, changed = %v", err, updated)
		}
		if corrected.Revision != first.Revision+1 || corrected.Plan.HistoryRevision != 3 {
			t.Fatalf("corrected draft = %+v", corrected)
		}
	})

	for _, state := range []string{"locked", "started"} {
		t.Run("rejects "+state+" round replacement", func(t *testing.T) {
			t.Parallel()

			participants := task027ParticipantIDs(5)
			history := []arena.SwissProgressionRound{task027OfficialRound(participants, 1)}
			plan, err := arena.PrepareSwissRoundProgression(task027ProgressionCommand(history, 2, 0, generatedAt))
			if err != nil {
				t.Fatalf("PrepareSwissRoundProgression() error = %v", err)
			}
			repository := newSwissProgressionRepositoryFake(plan)
			useCase := arena.NewSwissRoundProgressionUseCase(repository)
			created, changed, err := useCase.Apply(t.Context(), plan)
			if err != nil || !changed {
				t.Fatalf("Apply(create) error = %v, changed = %v", err, changed)
			}
			repository.setState(state, generatedAt.Add(time.Minute))

			correctedHistory := cloneTask027ProgressionHistory(history)
			correctedHistory[0].Series[0].ResultRevisionID = domain.ArenaOfficialResultRevisionID(task027ID(391))
			correctedHistory[0].Series[0].WinnerID = task027IDPointer(participants[1])
			command := task027ProgressionCommand(correctedHistory, 3, created.Revision, generatedAt.Add(2*time.Minute))
			command.RoundID = plan.RoundID
			correctedPlan, err := arena.PrepareSwissRoundProgression(command)
			if err != nil {
				t.Fatalf("PrepareSwissRoundProgression(corrected) error = %v", err)
			}
			repository.setAuthority(correctedPlan)

			if _, changed, err := useCase.Apply(t.Context(), correctedPlan); !errors.Is(err, arena.ErrSwissRoundNotEditable) || changed {
				t.Fatalf("Apply(%s) error = %v, changed = %v", state, err, changed)
			}
			if repository.snapshot().Revision != 1 {
				t.Fatalf("%s replacement changed revision to %d", state, repository.snapshot().Revision)
			}
		})
	}

	t.Run("serializes concurrent retries", func(t *testing.T) {
		t.Parallel()

		plan, err := arena.PrepareSwissRoundProgression(task027ProgressionCommand(nil, 1, 0, generatedAt))
		if err != nil {
			t.Fatalf("PrepareSwissRoundProgression() error = %v", err)
		}
		repository := newSwissProgressionRepositoryFake(plan)
		useCase := arena.NewSwissRoundProgressionUseCase(repository)
		var changedCount atomic.Int32
		errorsFound := make(chan error, 12)
		var group sync.WaitGroup
		for range 12 {
			group.Add(1)
			go func() {
				defer group.Done()
				_, changed, applyErr := useCase.Apply(t.Context(), plan)
				if applyErr != nil {
					errorsFound <- applyErr
					return
				}
				if changed {
					changedCount.Add(1)
				}
			}()
		}
		group.Wait()
		close(errorsFound)
		for applyErr := range errorsFound {
			t.Errorf("concurrent Apply() error = %v", applyErr)
		}
		if changedCount.Load() != 1 {
			t.Fatalf("changed count = %d, want 1", changedCount.Load())
		}
	})

	t.Run("stops after the preset Swiss round count", func(t *testing.T) {
		t.Parallel()

		participants := task027ParticipantIDs(5)
		history := []arena.SwissProgressionRound{
			task027OfficialRound(participants, 1),
			task027OfficialRound(participants, 2),
			task027OfficialRound(participants, 3),
		}
		command := task027ProgressionCommand(history, 4, 0, generatedAt)
		if _, err := arena.PrepareSwissRoundProgression(command); !errors.Is(err, arena.ErrSwissRoundsComplete) {
			t.Fatalf("PrepareSwissRoundProgression(complete) error = %v, want ErrSwissRoundsComplete", err)
		}
	})
}

func task027ProgressionCommand(
	history []arena.SwissProgressionRound,
	historyRevision int64,
	expectedRoundRevision int64,
	generatedAt time.Time,
) arena.SwissRoundProgressionCommand {
	const rosterSize = 5
	participants := task027ParticipantIDs(rosterSize)
	seeds := make([]arena.SwissParticipantSeed, rosterSize)
	for index, participantID := range participants {
		seeds[index] = arena.SwissParticipantSeed{ParticipantID: participantID, Seed: index + 1}
	}
	return arena.SwissRoundProgressionCommand{
		TournamentID: task027ID(900), RosterID: task027ID(901), RoundID: task027ID(910 + len(history)),
		Preset: domain.ArenaPresetV1, RosterParticipantIDs: participants, Seeds: seeds,
		HistoryRevision: historyRevision, History: cloneTask027ProgressionHistory(history),
		ExpectedRoundRevision: expectedRoundRevision,
		PairingEvidenceID:     task027ID(920 + int(historyRevision)),
		ByeEvidenceID:         task027ID(930 + int(historyRevision)),
		GeneratedAt:           generatedAt,
	}
}

func task027OfficialRound(participants []uuid.UUID, roundNumber int) arena.SwissProgressionRound {
	return arena.SwissProgressionRound{
		RoundID: task027ID(100 + roundNumber), RoundNumber: roundNumber,
		RevisionID: task027ID(150 + roundNumber),
		Series: []arena.SwissSeriesPointResult{
			task027SeriesResult(20+roundNumber*2, roundNumber, participants[0], participants[1], participants[0], arena.SwissSeriesResultPlayed, time.Minute, 3*time.Minute),
			task027SeriesResult(21+roundNumber*2, roundNumber, participants[2], participants[3], participants[2], arena.SwissSeriesResultNoShow, time.Minute, 3*time.Minute),
		},
		Bye: &arena.SwissByePointResult{
			RoundID: task027ID(100 + roundNumber), RoundNumber: roundNumber,
			ParticipantID: participants[4], RevisionID: task027ID(450 + roundNumber),
		},
	}
}

func cloneTask027ProgressionHistory(history []arena.SwissProgressionRound) []arena.SwissProgressionRound {
	cloned := make([]arena.SwissProgressionRound, len(history))
	for index, round := range history {
		cloned[index] = round
		cloned[index].Series = append([]arena.SwissSeriesPointResult(nil), round.Series...)
		for seriesIndex := range cloned[index].Series {
			cloned[index].Series[seriesIndex].WinnerID = cloneTask027UUIDPointer(round.Series[seriesIndex].WinnerID)
			cloned[index].Series[seriesIndex].FirstAcceptedSolveTime = cloneTask027DurationPointer(round.Series[seriesIndex].FirstAcceptedSolveTime)
			cloned[index].Series[seriesIndex].SecondAcceptedSolveTime = cloneTask027DurationPointer(round.Series[seriesIndex].SecondAcceptedSolveTime)
		}
		if round.Bye != nil {
			bye := *round.Bye
			cloned[index].Bye = &bye
		}
	}
	return cloned
}

func cloneTask027UUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTask027DurationPointer(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func assertProgressionCoverage(t *testing.T, plan arena.SwissRoundProgressionPlan) {
	t.Helper()
	seen := make(map[uuid.UUID]struct{}, len(plan.RosterParticipantIDs))
	for _, pairing := range plan.Pairing.Pairings {
		for _, participantID := range []uuid.UUID{pairing.FirstParticipantID, pairing.SecondParticipantID} {
			if _, duplicate := seen[participantID]; duplicate {
				t.Fatalf("participant %s appears twice", participantID)
			}
			seen[participantID] = struct{}{}
		}
	}
	if plan.Bye != nil {
		if _, duplicate := seen[plan.Bye.ParticipantID]; duplicate {
			t.Fatalf("bye participant %s also appears in pairing", plan.Bye.ParticipantID)
		}
		seen[plan.Bye.ParticipantID] = struct{}{}
	}
	if len(seen) != len(plan.RosterParticipantIDs) {
		t.Fatalf("plan covers %d participants, want %d", len(seen), len(plan.RosterParticipantIDs))
	}
}

type swissProgressionAuthority struct {
	historyRevision int64
	roundRevisions  []uuid.UUID
	resultRevisions []uuid.UUID
	byeRevisions    []uuid.UUID
}

type swissProgressionRepositoryFake struct {
	mu        sync.Mutex
	authority swissProgressionAuthority
	record    *arena.SwissRoundDraft
}

func newSwissProgressionRepositoryFake(plan arena.SwissRoundProgressionPlan) *swissProgressionRepositoryFake {
	return &swissProgressionRepositoryFake{authority: task027ProgressionAuthority(plan)}
}

func (f *swissProgressionRepositoryFake) SaveSwissRoundDraft(
	_ context.Context,
	input arena.SwissRoundProgressionInput,
) (*arena.SwissRoundDraft, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !task027AuthorityMatches(f.authority, input.Plan) {
		return nil, false, nil
	}
	if f.record != nil {
		if f.record.LockedAt != nil || f.record.StartedAt != nil {
			return cloneTask027SwissRoundDraft(f.record), false, nil
		}
		if f.record.Plan.ProofHash == input.Plan.ProofHash {
			return cloneTask027SwissRoundDraft(f.record), false, nil
		}
		if input.Plan.ExpectedRoundRevision != f.record.Revision {
			return nil, false, nil
		}
	} else if input.Plan.ExpectedRoundRevision != 0 {
		return nil, false, nil
	}
	revision := int64(1)
	if f.record != nil {
		revision = f.record.Revision + 1
	}
	f.record = &arena.SwissRoundDraft{Plan: input.Plan, Revision: revision}
	return cloneTask027SwissRoundDraft(f.record), true, nil
}

func (f *swissProgressionRepositoryFake) setAuthority(plan arena.SwissRoundProgressionPlan) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authority = task027ProgressionAuthority(plan)
}

func (f *swissProgressionRepositoryFake) setState(state string, value time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if state == "locked" {
		f.record.LockedAt = &value
	} else {
		f.record.StartedAt = &value
	}
}

func (f *swissProgressionRepositoryFake) snapshot() *arena.SwissRoundDraft {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneTask027SwissRoundDraft(f.record)
}

func task027ProgressionAuthority(plan arena.SwissRoundProgressionPlan) swissProgressionAuthority {
	return swissProgressionAuthority{
		historyRevision: plan.HistoryRevision,
		roundRevisions:  append([]uuid.UUID(nil), plan.SourceRoundRevisionIDs...),
		resultRevisions: append([]uuid.UUID(nil), plan.SourceResultRevisionIDs...),
		byeRevisions:    append([]uuid.UUID(nil), plan.SourceByeRevisionIDs...),
	}
}

func task027AuthorityMatches(authority swissProgressionAuthority, plan arena.SwissRoundProgressionPlan) bool {
	return authority.historyRevision == plan.HistoryRevision &&
		reflect.DeepEqual(authority.roundRevisions, plan.SourceRoundRevisionIDs) &&
		reflect.DeepEqual(authority.resultRevisions, plan.SourceResultRevisionIDs) &&
		reflect.DeepEqual(authority.byeRevisions, plan.SourceByeRevisionIDs)
}

func cloneTask027SwissRoundDraft(record *arena.SwissRoundDraft) *arena.SwissRoundDraft {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.LockedAt = cloneTask027TimePointer(record.LockedAt)
	cloned.StartedAt = cloneTask027TimePointer(record.StartedAt)
	return &cloned
}

func cloneTask027TimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
