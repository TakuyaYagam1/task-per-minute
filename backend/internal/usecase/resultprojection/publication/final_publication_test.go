package publication_test

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/publication"
)

func TestFinalPublicationValidation(t *testing.T) {
	t.Parallel()

	publication := finalPublicationFixture(t)
	require.NoError(t, publication.Validate())

	snapshot := publication.Snapshot()
	publication.Artifacts[0].Payload[0] ^= 0xff
	*publication.Artifacts[0].Members[0].ScoreMilli++
	*publication.Artifacts[3].Dependencies[0].DependsOnArtifactID = uuid.New()
	require.NoError(t, snapshot.Validate())

	t.Run("rejects stale or incomplete final authority", func(t *testing.T) {
		t.Parallel()

		tests := map[string]func(*projection.FinalPublication){
			"missing tournament revision": func(value *projection.FinalPublication) {
				value.Expected.TournamentRevision = 0
			},
			"negative projection revision": func(value *projection.FinalPublication) {
				value.Expected.ProjectionRevision = -1
			},
			"wrong champion": func(value *projection.FinalPublication) {
				value.Artifacts[3].Members[0].ParticipantID = uuid.New()
			},
			"missing bracket lineage": func(value *projection.FinalPublication) {
				value.Artifacts[3].Dependencies = value.Artifacts[3].Dependencies[1:]
			},
			"foreign result Series": func(value *projection.FinalPublication) {
				foreign := uuid.New()
				value.Artifacts[3].Dependencies[1].OfficialResultSeriesID = &foreign
			},
			"changed payload": func(value *projection.FinalPublication) {
				value.Artifacts[1].Payload = append(value.Artifacts[1].Payload, '\n')
			},
			"duplicate artifact": func(value *projection.FinalPublication) {
				value.Artifacts[3].ID = value.Artifacts[2].ID
			},
		}
		for name, mutate := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				invalid := finalPublicationFixture(t)
				mutate(&invalid)
				require.ErrorIs(t, invalid.Validate(), domain.ErrValidation)
			})
		}
	})

	t.Run("conflict exposes locked current revisions", func(t *testing.T) {
		t.Parallel()

		conflict := &projection.FinalRevisionConflictError{
			TournamentID: uuid.New(), ExpectedTournamentRevision: 8,
			CurrentTournamentRevision: 9, CurrentTournamentState: domain.TournamentStateCompleted,
			ExpectedProjectionRevision: 3, CurrentProjectionRevision: 4,
		}
		require.ErrorIs(t, conflict, domain.ErrConflict)
		require.Equal(t, int64(9), conflict.CurrentTournamentRevision)
		require.Equal(t, int64(4), conflict.CurrentProjectionRevision)
	})
}

func finalPublicationFixture(t *testing.T) projection.FinalPublication {
	t.Helper()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	seriesID := uuid.New()
	attemptID := uuid.New()
	winnerID := uuid.New()
	otherIDs := []uuid.UUID{winnerID, uuid.New(), uuid.New(), uuid.New()}
	seriesResultID := uuid.New()
	bracketID := uuid.New()
	standingsID := uuid.New()
	topFourID := uuid.New()
	standings := publicationArtifact(
		t,
		standingsID,
		domain.ArtifactKindStandings,
		map[string]any{"entries": []map[string]any{{"participant_id": winnerID}}},
		[]projection.PublicationMember{{ParticipantID: winnerID, Position: 1, ScoreMilli: publicationScore(2000)}},
		[]projection.PublicationDependency{{
			ID: uuid.New(), Kind: projection.DependencyOfficialResult,
			OfficialResultRevisionID: &seriesResultID, OfficialResultSeriesID: &seriesID,
		}},
	)
	bracket := publicationArtifact(
		t,
		bracketID,
		domain.ArtifactKindBracket,
		map[string]any{"rounds": []map[string]any{{"series_id": seriesID}}},
		[]projection.PublicationMember{{ParticipantID: winnerID, Position: 1}},
		[]projection.PublicationDependency{{
			ID: uuid.New(), Kind: projection.DependencyArtifact, DependsOnArtifactID: &standingsID,
		}},
	)
	topFourMembers := make([]projection.PublicationMember, len(otherIDs))
	for index, participantID := range otherIDs {
		topFourMembers[index] = projection.PublicationMember{
			ParticipantID: participantID,
			Position:      int32(index + 1),
		}
	}
	topFour := publicationArtifact(
		t,
		topFourID,
		domain.ArtifactKindTopFour,
		map[string]any{"participants": otherIDs},
		topFourMembers,
		[]projection.PublicationDependency{{
			ID: uuid.New(), Kind: projection.DependencyArtifact, DependsOnArtifactID: &bracketID,
		}},
	)
	champion := publicationArtifact(
		t,
		uuid.New(),
		domain.ArtifactKindChampion,
		map[string]any{"participant_id": winnerID},
		[]projection.PublicationMember{{ParticipantID: winnerID, Position: 1}},
		[]projection.PublicationDependency{
			{ID: uuid.New(), Kind: projection.DependencyArtifact, DependsOnArtifactID: &bracketID},
			{
				ID: uuid.New(), Kind: projection.DependencyOfficialResult,
				OfficialResultRevisionID: &seriesResultID, OfficialResultSeriesID: &seriesID,
			},
		},
	)
	createdAt := time.Date(2026, time.September, 6, 10, 0, 0, 0, time.UTC)
	return projection.FinalPublication{
		IDs: projection.PublicationIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: projection.FinalScope{
			TournamentID: tournamentID, RosterID: rosterID,
			SeriesID: seriesID, GameAttemptID: attemptID,
		},
		Expected: projection.FinalHeadExpectation{
			TournamentRevision: 8, ProjectionRevision: 3, GameAttemptRevision: 4,
			GameResultRevisionID: domain.OfficialResultRevisionID(uuid.New()), SeriesRevision: 9,
			ScoreHeadRevision: 4, ScoreRevisionID: domain.SeriesScoreRevisionID(uuid.New()),
			SeriesResultRevisionID: domain.OfficialResultRevisionID(seriesResultID), WinnerID: winnerID,
		},
		Artifacts:              []projection.PublicationArtifact{standings, bracket, topFour, champion},
		ResultProjectionDigest: sha256.Sum256([]byte("final-result-projection")),
		Reason:                 "final result changed the tournament projection",
		SupersessionReason:     "replaced by the terminal tournament projection",
		CutoffAt:               createdAt,
		CreatedAt:              createdAt,
		PublishedAt:            createdAt.Add(time.Second),
	}
}

func publicationArtifact(
	t *testing.T,
	id uuid.UUID,
	kind domain.ArtifactKind,
	payload any,
	members []projection.PublicationMember,
	dependencies []projection.PublicationDependency,
) projection.PublicationArtifact {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return projection.PublicationArtifact{
		ID: id, Kind: kind, Key: string(kind) + "-final", Payload: raw,
		PayloadDigest: sha256.Sum256(raw), Members: members, Dependencies: dependencies,
	}
}

func publicationScore(value int64) *int64 {
	return &value
}
