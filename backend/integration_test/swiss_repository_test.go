//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	swissrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/swiss"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestSwissRepository(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	fixture := newRepositoryFixture()
	baseTime := time.Now().UTC().Truncate(time.Microsecond)
	tournament, roster := createRepositoryTournament(ctx, t, fixture, baseTime)
	playerIDs := createMigrationPlayers(ctx, t, 5)
	participantIDs := make([]uuid.UUID, len(playerIDs))
	seed := int32(1)
	for index, playerID := range playerIDs {
		participant := addRepositoryParticipant(
			ctx, t,
			fixture,
			roster.ID,
			playerID,
			seed,
			domain.AttendanceStateCheckedIn,
			baseTime.Add(time.Second),
		)
		participantIDs[index] = participant.ID
		seed++
	}

	roundOneID := uuid.New()
	roundOneGeneratedAt := baseTime.Add(2 * time.Second)
	roundOneBye := selectRepositoryBye(
		t,
		roundOneID,
		participantIDs,
		nil,
		roundOneGeneratedAt,
	)
	roundOneEligible := withoutParticipant(participantIDs, roundOneBye.ParticipantID)
	roundOnePairing, err := swissusecase.GenerateAutomaticPairing(
		uuid.New(),
		roundOneID,
		roundOneEligible,
		nil,
		roundOneGeneratedAt,
	)
	require.NoError(t, err)
	automaticInput := swissrepo.AutomaticSwissRoundInput{
		Meta: swissrepo.SwissRoundMeta{
			ID:                    roundOneID,
			TournamentID:          tournament.ID,
			RosterID:              roster.ID,
			RoundNumber:           1,
			SourceRosterRevision:  roster.Revision,
			SourceHistoryRevision: 0,
			PairingIDs:            newUUIDs(len(roundOnePairing.Pairings)),
			PriorMeetingCounts:    make([]int16, len(roundOnePairing.Pairings)),
			Bye:                   &roundOneBye,
			GeneratedAt:           roundOneGeneratedAt,
			RecordedAt:            roundOneGeneratedAt.Add(time.Second),
		},
		Pairing: roundOnePairing,
	}
	automaticRecord, err := fixture.swiss.SaveAutomaticRound(ctx, automaticInput, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, automaticRecord.Revision)
	require.Nil(t, automaticRecord.LockedAt, "a newly persisted editable round must remain unlocked")

	loadedAutomatic, err := fixture.swiss.Get(ctx, roundOneID)
	require.NoError(t, err)
	require.Equal(t, "automatic", loadedAutomatic.Round.GenerationKind)
	require.Len(t, loadedAutomatic.Pairings, 2)
	require.NotNil(t, loadedAutomatic.Bye)
	require.Equal(t, roundOneBye.ParticipantID, loadedAutomatic.Bye.ParticipantID)
	require.NotNil(t, loadedAutomatic.Round.AutomaticEvidence)
	_, err = swissusecase.ReplayAutomaticPairing(swissusecase.AutomaticPairing{
		Pairings: swissPairs(loadedAutomatic.Pairings),
		Evidence: *loadedAutomatic.Round.AutomaticEvidence,
	})
	require.NoError(t, err)

	lockedAutomatic, changed, err := fixture.swiss.LockRound(
		ctx,
		roundOneID,
		automaticRecord.Revision,
		baseTime.Add(6*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, lockedAutomatic.LockedAt)
	require.NotNil(t, lockedAutomatic.LockRevision)
	require.Equal(t, lockedAutomatic.Revision, *lockedAutomatic.LockRevision)

	_, err = fixture.swiss.SaveAutomaticRound(ctx, automaticInput, &automaticRecord.Revision)
	require.ErrorIs(t, err, domain.ErrConflict, "locked rounds must reject every edit")

	roundTwoID := uuid.New()
	roundTwoGeneratedAt := baseTime.Add(7 * time.Second)
	roundTwoBye := selectRepositoryBye(
		t,
		roundTwoID,
		participantIDs,
		map[uuid.UUID]bool{roundOneBye.ParticipantID: true},
		roundTwoGeneratedAt,
	)
	require.NotEqual(t, roundOneBye.ParticipantID, roundTwoBye.ParticipantID)
	repeated := pairingWithoutParticipant(roundOnePairing.Pairings, roundTwoBye.ParticipantID)
	remainder := withoutParticipants(
		participantIDs,
		roundTwoBye.ParticipantID,
		repeated.FirstParticipantID,
		repeated.SecondParticipantID,
	)
	require.Len(t, remainder, 2)
	manualRound := swissusecase.ManualRound{
		ID: roundTwoID,
		Pairings: []swissusecase.Pair{
			repeated,
			{FirstParticipantID: remainder[0], SecondParticipantID: remainder[1]},
		},
		ByeParticipantID: roundTwoBye.ParticipantID,
	}
	override, overrideChanged, err := swissusecase.ConfirmRepeatOverride(
		nil,
		swissusecase.RepeatOverrideCommand{
			ID:                   uuid.New(),
			ActorID:              uuid.New(),
			Confirmed:            true,
			Reason:               "complete search confirms the repeat is required",
			ConfirmedAt:          roundTwoGeneratedAt,
			RosterParticipantIDs: participantIDs,
			Round:                manualRound,
			PreviousMeetings:     roundOnePairing.Pairings,
		},
	)
	require.NoError(t, err)
	require.True(t, overrideChanged)
	manualInput := swissrepo.ManualSwissRoundInput{
		Meta: swissrepo.SwissRoundMeta{
			ID:                    roundTwoID,
			TournamentID:          tournament.ID,
			RosterID:              roster.ID,
			RoundNumber:           2,
			SourceRosterRevision:  roster.Revision,
			SourceHistoryRevision: 1,
			PairingIDs:            newUUIDs(len(manualRound.Pairings)),
			PriorMeetingCounts:    []int16{1, 0},
			Bye:                   &roundTwoBye,
			GeneratedAt:           roundTwoGeneratedAt,
			RecordedAt:            roundTwoGeneratedAt.Add(time.Second),
		},
		Round:         manualRound,
		PairingInputs: []string{"manual-roster-snapshot", "prior-meeting-snapshot"},
		Override:      &override,
	}
	manualRecord, err := fixture.swiss.SaveManualRound(ctx, manualInput, nil)
	require.NoError(t, err)
	require.Nil(t, manualRecord.LockedAt)
	require.EqualValues(t, 1, manualRecord.Revision)

	roundTwoLockedAt := roundTwoGeneratedAt.Add(3 * time.Second)
	manualInput.Meta.PairingIDs = newUUIDs(len(manualRound.Pairings))
	manualInput.Meta.RecordedAt = roundTwoGeneratedAt.Add(2 * time.Second)
	manualInput.Meta.LockedAt = &roundTwoLockedAt
	expectedManualRevision := manualRecord.Revision
	manualRecord, err = fixture.swiss.SaveManualRound(ctx, manualInput, &expectedManualRevision)
	require.NoError(t, err)
	require.NotNil(t, manualRecord.LockedAt, "a complete round may be persisted and locked atomically")
	require.EqualValues(t, 2, manualRecord.Revision)

	_, err = fixture.swiss.SaveManualRound(ctx, manualInput, &manualRecord.Revision)
	require.ErrorIs(t, err, domain.ErrConflict, "a locked manual round must reject every edit")

	loadedManual, err := fixture.swiss.Get(ctx, roundTwoID)
	require.NoError(t, err)
	require.Equal(t, "manual", loadedManual.Round.GenerationKind)
	require.Nil(t, loadedManual.Round.AutomaticEvidence)
	require.NotNil(t, loadedManual.Override)
	require.Equal(t, override.CommandID, loadedManual.Override.CommandID)
	require.Equal(t, override.Reason, loadedManual.Override.Reason)
	require.NotNil(t, loadedManual.Bye)
	require.Equal(t, roundTwoBye.ParticipantID, loadedManual.Bye.ParticipantID)
	require.Equal(t, 1, loadedManual.Pairings[0].PriorMeetingCount)
	require.Equal(t, override.CommandID, *loadedManual.Pairings[0].RepeatOverrideID)
	require.Zero(t, loadedManual.Pairings[1].PriorMeetingCount)
	require.Nil(t, loadedManual.Pairings[1].RepeatOverrideID)

	history, err := fixture.swiss.ListOpponentHistory(ctx, roster.ID)
	require.NoError(t, err)
	require.Len(t, history, 4)
	require.Equal(t, roundOneID, history[0].RoundID)
	require.Equal(t, roundTwoID, history[2].RoundID)

	roundThreeID := uuid.New()
	roundThreeGeneratedAt := baseTime.Add(10 * time.Second)
	duplicateBye := selectRepositoryBye(
		t,
		roundThreeID,
		participantIDs,
		onlyParticipantEligible(participantIDs, roundOneBye.ParticipantID),
		roundThreeGeneratedAt,
	)
	require.Equal(t, roundOneBye.ParticipantID, duplicateBye.ParticipantID)
	roundThreePairing, err := swissusecase.GenerateAutomaticPairing(
		uuid.New(),
		roundThreeID,
		withoutParticipant(participantIDs, duplicateBye.ParticipantID),
		nil,
		roundThreeGeneratedAt,
	)
	require.NoError(t, err)
	_, err = fixture.swiss.SaveAutomaticRound(ctx, swissrepo.AutomaticSwissRoundInput{
		Meta: swissrepo.SwissRoundMeta{
			ID:                    roundThreeID,
			TournamentID:          tournament.ID,
			RosterID:              roster.ID,
			RoundNumber:           3,
			SourceRosterRevision:  roster.Revision,
			SourceHistoryRevision: 2,
			PairingIDs:            newUUIDs(len(roundThreePairing.Pairings)),
			PriorMeetingCounts:    make([]int16, len(roundThreePairing.Pairings)),
			Bye:                   &duplicateBye,
			GeneratedAt:           roundThreeGeneratedAt,
			RecordedAt:            roundThreeGeneratedAt.Add(time.Second),
		},
		Pairing: roundThreePairing,
	}, nil)
	require.ErrorIs(t, err, domain.ErrConflict)
	_, err = fixture.swiss.Get(ctx, roundThreeID)
	require.ErrorIs(t, err, swissrepo.ErrSwissRoundNotFound,
		"a late child constraint failure must roll back the parent and all earlier children")

	rounds, err := fixture.swiss.ListRounds(ctx, roster.ID)
	require.NoError(t, err)
	require.Len(t, rounds, 2)
}

func selectRepositoryBye(
	tb testing.TB,
	roundID uuid.UUID,
	participantIDs []uuid.UUID,
	received map[uuid.UUID]bool,
	decidedAt time.Time,
) swissusecase.ByeSelection {
	tb.Helper()
	candidates := make([]swissusecase.ByeCandidate, len(participantIDs))
	for index, participantID := range participantIDs {
		candidates[index] = swissusecase.ByeCandidate{
			ParticipantID:       participantID,
			Points:              index,
			ProvisionalBuchholz: index,
			EffectiveTime:       time.Duration(index) * time.Second,
			ReceivedBye:         received[participantID],
		}
	}
	selection, err := swissusecase.SelectBye(uuid.New(), roundID, candidates, decidedAt)
	require.NoError(tb, err)
	return selection
}

func newUUIDs(count int) []uuid.UUID {
	values := make([]uuid.UUID, count)
	for index := range values {
		values[index] = uuid.New()
	}
	return values
}

func withoutParticipant(participantIDs []uuid.UUID, excluded uuid.UUID) []uuid.UUID {
	return withoutParticipants(participantIDs, excluded)
}

func withoutParticipants(participantIDs []uuid.UUID, excluded ...uuid.UUID) []uuid.UUID {
	blocked := make(map[uuid.UUID]struct{}, len(excluded))
	for _, participantID := range excluded {
		blocked[participantID] = struct{}{}
	}
	out := make([]uuid.UUID, 0, len(participantIDs)-len(excluded))
	for _, participantID := range participantIDs {
		if _, skip := blocked[participantID]; !skip {
			out = append(out, participantID)
		}
	}
	return out
}

func pairingWithoutParticipant(pairings []swissusecase.Pair, participantID uuid.UUID) swissusecase.Pair {
	for _, pairing := range pairings {
		if pairing.FirstParticipantID != participantID && pairing.SecondParticipantID != participantID {
			return pairing
		}
	}
	return swissusecase.Pair{}
}

func swissPairs(records []swissrepo.SwissPairingRecord) []swissusecase.Pair {
	pairs := make([]swissusecase.Pair, len(records))
	for index, record := range records {
		pairs[index] = record.Pair
	}
	return pairs
}

func onlyParticipantEligible(participantIDs []uuid.UUID, eligible uuid.UUID) map[uuid.UUID]bool {
	received := make(map[uuid.UUID]bool, len(participantIDs))
	for _, participantID := range participantIDs {
		received[participantID] = participantID != eligible
	}
	return received
}
