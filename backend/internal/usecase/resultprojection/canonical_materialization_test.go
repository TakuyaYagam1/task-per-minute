package resultprojection_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestBuildCanonicalMaterializationUsesSwissTieBreaks(t *testing.T) {
	t.Parallel()

	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	createdAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	firstRound := uuid.New()
	secondRound := uuid.New()
	materialized, err := projection.BuildCanonicalMaterialization(projection.CanonicalMaterializationInput{
		TournamentID: uuid.New(),
		Participants: []projection.CanonicalSwissParticipant{
			{ID: participants[0], StableSeed: 4},
			{ID: participants[1], StableSeed: 1},
			{ID: participants[2], StableSeed: 2},
			{ID: participants[3], StableSeed: 3},
		},
		SwissLedger: canonicalSwissLedger([]swissusecase.Round{
			{
				RoundID: firstRound, RoundNumber: 1, RevisionID: uuid.New(),
				Series: []swissusecase.SeriesPointResult{
					canonicalSwissSeries(firstRound, 1, participants[0], participants[1], participants[0], createdAt),
					canonicalSwissSeries(firstRound, 1, participants[2], participants[3], participants[2], createdAt),
				},
			},
			{
				RoundID: secondRound, RoundNumber: 2, RevisionID: uuid.New(),
				Series: []swissusecase.SeriesPointResult{
					canonicalSwissSeries(secondRound, 2, participants[0], participants[2], participants[2], createdAt),
					canonicalSwissSeries(secondRound, 2, participants[1], participants[3], participants[1], createdAt),
				},
			},
		}, map[uuid.UUID]int{
			participants[0]: 4, participants[1]: 1, participants[2]: 2, participants[3]: 3,
		}),
		SwissComplete: true,
		TopFour: []projection.CanonicalTopFourPosition{
			{ParticipantID: participants[2], Position: 1}, {ParticipantID: participants[0], Position: 2},
			{ParticipantID: participants[1], Position: 3}, {ParticipantID: participants[3], Position: 4},
		},
		ArtifactKinds: []domain.ArtifactKind{
			domain.ArtifactKindStandings,
			domain.ArtifactKindTopFour,
		},
	})
	require.NoError(t, err)
	require.Len(t, materialized.Artifacts, 2)

	var standings struct {
		Entries []struct {
			ParticipantID uuid.UUID `json:"participant_id"`
			Position      int       `json:"position"`
			Points        int       `json:"points"`
			Buchholz      int       `json:"buchholz"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(materialized.Artifacts[0].Payload, &standings))
	require.Equal(t, participants[2], standings.Entries[0].ParticipantID)
	// The old UUID fallback would put participant 1 ahead of participant 0.
	require.Equal(t, participants[0], standings.Entries[1].ParticipantID)
	require.Equal(t, 3, standings.Entries[1].Buchholz)
	require.Equal(t, 1, standings.Entries[1].Points)
}

func TestBuildCanonicalMaterializationDoesNotInventLaterStages(t *testing.T) {
	t.Parallel()

	_, err := projection.BuildCanonicalMaterialization(projection.CanonicalMaterializationInput{
		TournamentID: uuid.New(),
		Participants: []projection.CanonicalSwissParticipant{
			{ID: uuid.New(), StableSeed: 1}, {ID: uuid.New(), StableSeed: 2},
			{ID: uuid.New(), StableSeed: 3}, {ID: uuid.New(), StableSeed: 4},
		},
		ArtifactKinds: []domain.ArtifactKind{domain.ArtifactKindTopFour},
	})
	require.ErrorIs(t, err, projection.ErrInvalidCanonicalMaterialization)
}

func TestBuildCanonicalMaterializationRejectsTopFourOverrideWithoutGoldenCommit(t *testing.T) {
	t.Parallel()

	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	roundID, roundRevisionID := uuid.New(), uuid.New()
	createdAt := time.Date(2026, time.September, 6, 12, 30, 0, 0, time.UTC)
	round := swissusecase.Round{
		RoundID: roundID, RoundNumber: 1, RevisionID: roundRevisionID,
		Series: []swissusecase.SeriesPointResult{
			canonicalSwissSeries(roundID, 1, participants[0], participants[1], participants[0], createdAt),
			canonicalSwissSeries(roundID, 1, participants[2], participants[3], participants[2], createdAt),
		},
	}
	_, err := projection.BuildCanonicalMaterialization(projection.CanonicalMaterializationInput{
		TournamentID: uuid.New(),
		Participants: []projection.CanonicalSwissParticipant{
			{ID: participants[0], StableSeed: 1}, {ID: participants[1], StableSeed: 3},
			{ID: participants[2], StableSeed: 2}, {ID: participants[3], StableSeed: 4},
		},
		SwissLedger: canonicalSwissLedger([]swissusecase.Round{round}, map[uuid.UUID]int{
			participants[0]: 1, participants[1]: 3, participants[2]: 2, participants[3]: 4,
		}),
		SwissComplete: true,
		TopFour: []projection.CanonicalTopFourPosition{
			{ParticipantID: participants[0], Position: 1}, {ParticipantID: participants[1], Position: 2},
			{ParticipantID: participants[2], Position: 3}, {ParticipantID: participants[3], Position: 4},
		},
		ArtifactKinds: []domain.ArtifactKind{domain.ArtifactKindStandings, domain.ArtifactKindTopFour},
	})
	require.ErrorIs(t, err, projection.ErrInvalidCanonicalMaterialization)
}

func TestBuildCanonicalMaterializationRejectsBracketOutsideQualifiedPositions(t *testing.T) {
	t.Parallel()

	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	roundID, roundRevisionID := uuid.New(), uuid.New()
	firstSeries := canonicalSwissSeries(roundID, 1, participants[0], participants[1], participants[0], time.Date(2026, time.September, 6, 13, 0, 0, 0, time.UTC))
	secondSeries := canonicalSwissSeries(roundID, 1, participants[2], participants[3], participants[2], time.Date(2026, time.September, 6, 13, 0, 0, 0, time.UTC))
	_, err := projection.BuildCanonicalMaterialization(projection.CanonicalMaterializationInput{
		TournamentID: uuid.New(),
		Participants: []projection.CanonicalSwissParticipant{
			{ID: participants[0], StableSeed: 1}, {ID: participants[1], StableSeed: 2},
			{ID: participants[2], StableSeed: 3}, {ID: participants[3], StableSeed: 4},
		},
		SwissLedger: canonicalSwissLedger([]swissusecase.Round{{
			RoundID: roundID, RoundNumber: 1, RevisionID: roundRevisionID,
			Series: []swissusecase.SeriesPointResult{firstSeries, secondSeries},
		}}, map[uuid.UUID]int{
			participants[0]: 1, participants[1]: 2, participants[2]: 3, participants[3]: 4,
		}),
		SwissComplete: true,
		TopFour: []projection.CanonicalTopFourPosition{
			{ParticipantID: participants[0], Position: 1}, {ParticipantID: participants[2], Position: 2},
			{ParticipantID: participants[1], Position: 3}, {ParticipantID: participants[3], Position: 4},
		},
		Bracket: []projection.CanonicalBracketMatch{{
			Position: 1, SeriesID: uuid.New(), FirstParticipantID: participants[0], SecondParticipantID: uuid.New(),
			State: domain.SeriesStatePlanned,
		}},
		ArtifactKinds: []domain.ArtifactKind{
			domain.ArtifactKindStandings, domain.ArtifactKindTopFour, domain.ArtifactKindBracket,
		},
	})
	require.ErrorIs(t, err, projection.ErrInvalidCanonicalMaterialization)
}

func canonicalSwissSeries(
	roundID uuid.UUID,
	roundNumber int,
	first, second, winner uuid.UUID,
	_ time.Time,
) swissusecase.SeriesPointResult {
	winnerID := winner
	return swissusecase.SeriesPointResult{
		RoundID: roundID, RoundNumber: roundNumber, SeriesID: uuid.New(),
		ResultRevisionID:   domain.OfficialResultRevisionID(uuid.New()),
		FirstParticipantID: first, SecondParticipantID: second, WinnerID: &winnerID,
		Label: swissusecase.SeriesResultPlayed, FirstEffectiveTime: time.Minute,
		SecondEffectiveTime: time.Minute, FirstAcceptedSolveTime: canonicalDuration(time.Second),
		SecondAcceptedSolveTime: canonicalDuration(time.Second),
	}
}

func canonicalDuration(value time.Duration) *time.Duration {
	return &value
}

func canonicalSwissLedger(
	rounds []swissusecase.Round,
	seeds map[uuid.UUID]int,
) []projection.CanonicalSwissPointLedgerEntry {
	entries := make([]projection.CanonicalSwissPointLedgerEntry, 0)
	for _, round := range rounds {
		for _, series := range round.Series {
			firstPoints, secondPoints := 0, 0
			if series.WinnerID != nil {
				if *series.WinnerID == series.FirstParticipantID {
					firstPoints = swissusecase.SeriesWinPoints
				} else {
					secondPoints = swissusecase.SeriesWinPoints
				}
			}
			firstOpponent, secondOpponent := series.SecondParticipantID, series.FirstParticipantID
			entries = append(entries,
				projection.CanonicalSwissPointLedgerEntry{
					RoundID: round.RoundID, RoundRevisionID: round.RevisionID, RoundNumber: round.RoundNumber,
					SourceKind: swissusecase.PointSourceSeries, SourceSeriesID: series.SeriesID,
					SeriesResultRevisionID: series.ResultRevisionID.UUID(), ResultLabel: series.Label,
					ParticipantID: series.FirstParticipantID, OpponentID: &firstOpponent, Points: firstPoints,
					EffectiveTime: series.FirstEffectiveTime, AcceptedSolveTime: series.FirstAcceptedSolveTime,
					StableSeed: seeds[series.FirstParticipantID],
				},
				projection.CanonicalSwissPointLedgerEntry{
					RoundID: round.RoundID, RoundRevisionID: round.RevisionID, RoundNumber: round.RoundNumber,
					SourceKind: swissusecase.PointSourceSeries, SourceSeriesID: series.SeriesID,
					SeriesResultRevisionID: series.ResultRevisionID.UUID(), ResultLabel: series.Label,
					ParticipantID: series.SecondParticipantID, OpponentID: &secondOpponent, Points: secondPoints,
					EffectiveTime: series.SecondEffectiveTime, AcceptedSolveTime: series.SecondAcceptedSolveTime,
					StableSeed: seeds[series.SecondParticipantID],
				},
			)
		}
		if round.Bye != nil {
			entries = append(entries, projection.CanonicalSwissPointLedgerEntry{
				RoundID: round.RoundID, RoundRevisionID: round.RevisionID, RoundNumber: round.RoundNumber,
				SourceKind: swissusecase.PointSourceBye, ByeRevisionID: round.Bye.RevisionID,
				ParticipantID: round.Bye.ParticipantID, Points: swissusecase.StandingsByePoints, StableSeed: seeds[round.Bye.ParticipantID],
			})
		}
	}
	return entries
}
