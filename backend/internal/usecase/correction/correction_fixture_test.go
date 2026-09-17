package correction_test

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func task056CorrectionFixture(t *testing.T) (correctionusecase.Command, correctionusecase.Authority) {
	t.Helper()
	fixture := correctionDAGTestRevisionDAGFixture(t)
	dag, err := resultprojection.BuildRevisionDAG(fixture)
	require.NoError(t, err)
	gameHead := fixture.Results[0].Result.Clone()
	scoreHead := fixture.Results[2].Score.Clone()
	seriesHead := fixture.Results[2].Result.Clone()
	series := task056Series(fixture, gameHead, scoreHead, seriesHead)
	tournamentID := gameHead.Scope.TournamentID
	readiness := correctionusecase.Readiness{
		TournamentID: tournamentID, WaveID: correctionDAGTestID(40_001), WindowID: correctionDAGTestID(40_002),
		RevisionID: correctionDAGTestID(40_003), Revision: 4, State: correctionusecase.ReadinessOpen,
		OwnerID:        series.ID,
		ParticipantIDs: []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID},
	}
	reservations := []correctionusecase.Reservation{
		{
			ID: correctionDAGTestID(40_010), TournamentID: tournamentID, OwnerID: series.ID,
			SourceRevisionID: gameHead.SourceProjection.ID(), Revision: 3,
			EvidenceDigest: sha256.Sum256([]byte("unused-undisclosed")),
		},
		{
			ID: correctionDAGTestID(40_011), TournamentID: tournamentID, OwnerID: series.ID,
			SourceRevisionID: seriesHead.SourceProjection.ID(), Revision: 2, Disclosed: true,
			EvidenceDigest: sha256.Sum256([]byte("disclosed")),
		},
		{
			ID: correctionDAGTestID(40_012), TournamentID: tournamentID, OwnerID: series.ID,
			SourceRevisionID: scoreHead.SourceProjection.ID(), Revision: 1, Used: true,
			EvidenceDigest: sha256.Sum256([]byte("used")),
		},
		{
			ID: correctionDAGTestID(40_013), TournamentID: tournamentID, OwnerID: series.ID,
			SourceRevisionID: fixture.Results[1].Result.SourceProjection.ID(), Revision: 1,
			EvidenceDigest: sha256.Sum256([]byte("unrelated")),
		},
	}
	decisions := task056RecordedDecisions(t, fixture)
	solvedAt := gameHead.RecordedAt.Add(-time.Second)
	currentSolve := correctionusecase.SolveMetadata{
		SolvedAt: &solvedAt, SubmissionID: correctionDAGTestUUIDPointer(correctionDAGTestID(40_020)),
		EvidenceDigest: sha256.Sum256([]byte("verified solve")),
	}
	authority := correctionusecase.Authority{
		TournamentState: domain.TournamentStatePlayoffs, TournamentRevision: 11,
		DAG: dag, Series: series, GameResult: gameHead, Score: scoreHead, SeriesResult: seriesHead,
		SeriesRevision: 9, AttemptRevision: 7, CurrentSolve: currentSolve,
		Readiness: readiness, Reservations: reservations, Decisions: decisions,
	}
	expected, err := correctionusecase.NewExpectation(authority, gameHead.SourceProjection.ID())
	require.NoError(t, err)
	cutoff, err := correctionusecase.EvaluateCutoff(correctionusecase.CutoffInput{
		DAG: authority.DAG, TournamentID: tournamentID,
		TargetRevisionID: gameHead.SourceProjection.ID(), TournamentState: authority.TournamentState,
	})
	require.NoError(t, err)
	revisions := append([]domain.DerivedRevision{expected.TargetProjection}, cutoff.Descendants()...)
	projectionIntents := make([]correctionusecase.ProjectionIntent, len(revisions))
	for index, revision := range revisions {
		projectionIntents[index] = correctionusecase.NewProjectionIntent(
			revision, domain.DerivedRevisionID(correctionDAGTestID(40_100+index)),
			correctionDAGTestID(40_200+index), fmt.Appendf(nil, "correction-%02d", index),
		)
	}
	command := correctionusecase.Command{
		TournamentID: tournamentID, SeriesID: series.ID, GameID: gameHead.Scope.GameID,
		CommandID: correctionDAGTestID(40_300), CascadeCommandID: correctionDAGTestID(40_301),
		OperatorID: correctionDAGTestID(40_302), Confirmed: true,
		Reason: correctionusecase.ReasonOperatorRuling, Explanation: "Verified referee ruling.",
		RequestedAt: gameHead.RecordedAt.Add(time.Minute),
		Expected:    expected,
		Patch: correctionusecase.Patch{
			State:    domain.GameStateCompleted,
			Reason:   domain.GameResultReasonSurrender,
			WinnerID: correctionDAGTestUUIDPointer(series.FirstParticipantID),
		},
		Fields: []correctionusecase.Field{
			correctionusecase.FieldResultReason,
			correctionusecase.FieldSolveMetadata,
		},
		NextResultRevisionID:       domain.OfficialResultRevisionID(correctionDAGTestID(40_310)),
		NextScoreRevisionID:        domain.SeriesScoreRevisionID(correctionDAGTestID(40_311)),
		NextSeriesResultRevisionID: domain.OfficialResultRevisionID(correctionDAGTestID(40_312)),
		NextReadinessRevisionID:    correctionDAGTestID(40_313),
		ProjectionIntents:          projectionIntents,
		UnlockIntents: []correctionusecase.UnlockIntent{
			correctionusecase.NewUnlockIntent(reservations[0]),
		},
	}
	return command, authority
}

func task056Series(
	fixture resultprojection.RevisionDAGInput,
	gameHead resultusecase.OfficialResultRevisionHead,
	scoreHead resultusecase.SeriesScoreRevisionHead,
	seriesHead resultusecase.OfficialResultRevisionHead,
) domain.Series {
	slots := make([]domain.GameSlot, len(scoreHead.Attempts))
	for index, attempt := range scoreHead.Attempts {
		scoreBefore := domain.SeriesScore{FirstParticipantWins: index}
		slots[index] = domain.GameSlot{
			ID: attempt.SlotID, SeriesID: gameHead.Scope.SeriesID, Position: attempt.SlotPosition,
			Category: domain.CategoryWeb, ScoreBefore: scoreBefore,
			Attempts: []domain.Game{{
				ID: attempt.GameID, SlotID: attempt.SlotID, AttemptNo: attempt.AttemptNo,
				State: attempt.State, ResultReason: attempt.Reason,
				WinnerID: correctionDAGTestUUIDPointerOrNil(attempt.WinnerID),
				ResultRevisionID: func() *domain.OfficialResultRevisionID {
					value := attempt.CurrentGameResultRevisionID
					return &value
				}(),
			}},
		}
	}
	series := domain.Series{
		ID: gameHead.Scope.SeriesID, TournamentID: gameHead.Scope.TournamentID,
		FirstParticipantID:  scoreHead.FirstParticipantID,
		SecondParticipantID: scoreHead.SecondParticipantID,
		Format:              scoreHead.Format, State: domain.SeriesStateCompleted,
		Score: scoreHead.Score, WinnerID: correctionDAGTestUUIDPointerOrNil(seriesHead.Outcome.WinnerID),
		Slots: slots, CurrentScoreRevisionID: correctionDAGTestScoreIDPointer(scoreHead.ID),
		CurrentResultRevisionID: func() *domain.OfficialResultRevisionID {
			value := seriesHead.ID
			return &value
		}(),
	}
	_ = fixture
	return series
}

func task056RecordedDecisions(
	t *testing.T,
	fixture resultprojection.RevisionDAGInput,
) []resultprojection.RecordedProjectionDecision {
	t.Helper()
	decisions := []resultprojection.RecordedProjectionDecision{
		{
			ID: correctionDAGTestID(40_030), Sequence: 1,
			ProjectionRevisionID: correctionDAGTestCurrentProjectionID(t, fixture.Graph, domain.ArtifactKindTopFour),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 8, 0, time.UTC),
			Payload:              []byte(`{"order":[1,2,3,4]}`),
		},
		{
			ID: correctionDAGTestID(40_031), Sequence: 2,
			ProjectionRevisionID: correctionDAGTestCurrentProjectionID(t, fixture.Graph, domain.ArtifactKindChampion),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:              []byte(`{"winner":"recorded"}`),
		},
	}
	for index := range decisions {
		decisions[index].PayloadDigest = sha256.Sum256(decisions[index].Payload)
	}
	return decisions
}

func task056CloneCommand(command correctionusecase.Command) correctionusecase.Command {
	clone := command
	clone.Patch.WinnerID = correctionDAGTestUUIDPointerOrNil(command.Patch.WinnerID)
	clone.Patch.SolveMetadata = command.Patch.SolveMetadata.Clone()
	clone.Fields = append([]correctionusecase.Field(nil), command.Fields...)
	clone.ProjectionIntents = make([]correctionusecase.ProjectionIntent, len(command.ProjectionIntents))
	for index := range command.ProjectionIntents {
		clone.ProjectionIntents[index] = command.ProjectionIntents[index].Clone()
	}
	clone.UnlockIntents = append([]correctionusecase.UnlockIntent(nil), command.UnlockIntents...)
	return clone
}

func task056CloneAuthority(authority correctionusecase.Authority) correctionusecase.Authority {
	clone := authority
	clone.Reservations = append([]correctionusecase.Reservation(nil), authority.Reservations...)
	clone.Decisions = make([]resultprojection.RecordedProjectionDecision, len(authority.Decisions))
	for index := range authority.Decisions {
		clone.Decisions[index] = authority.Decisions[index]
		clone.Decisions[index].Payload = append([]byte(nil), authority.Decisions[index].Payload...)
	}
	return clone
}
