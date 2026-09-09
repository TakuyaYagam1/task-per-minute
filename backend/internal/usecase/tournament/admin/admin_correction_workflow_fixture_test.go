package admin_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func correctionWorkflowFixture(
	t *testing.T,
) (tournamentadmin.CorrectionCommand, tournamentadmin.CorrectionWorkflowAuthority, time.Time) {
	t.Helper()

	base := time.Date(2026, time.September, 6, 9, 0, 0, 0, time.UTC)
	tournamentID := correctionWorkflowID(1)
	seriesID := correctionWorkflowID(2)
	gameID := correctionWorkflowID(3)
	firstID := correctionWorkflowID(4)
	secondID := correctionWorkflowID(5)
	slotID := correctionWorkflowID(6)

	gameProjection := correctionWorkflowProjection(
		t, correctionWorkflowID(10), tournamentID, domain.ArtifactKindGameResult,
		gameID, base, []byte(`{"state":"completed","winner":"first"}`),
	)
	scoreProjection := correctionWorkflowProjection(
		t, correctionWorkflowID(11), tournamentID, domain.ArtifactKindSeriesScore,
		seriesID, base.Add(time.Second), []byte(`{"first":1,"second":0}`),
	)
	seriesProjection := correctionWorkflowProjection(
		t, correctionWorkflowID(12), tournamentID, domain.ArtifactKindSeriesResult,
		seriesID, base.Add(2*time.Second), []byte(`{"state":"completed","winner":"first"}`),
	)
	standingsProjection := correctionWorkflowProjection(
		t, correctionWorkflowID(13), tournamentID, domain.ArtifactKindStandings,
		tournamentID, base.Add(3*time.Second), []byte(`{"entries":[{"participant":"first"}]}`),
	)
	topFourProjection := correctionWorkflowProjection(
		t, correctionWorkflowID(14), tournamentID, domain.ArtifactKindTopFour,
		tournamentID, base.Add(4*time.Second), []byte(`{"participants":["first","second"]}`),
	)
	bracketProjection := correctionWorkflowProjection(
		t, correctionWorkflowID(15), tournamentID, domain.ArtifactKindBracket,
		tournamentID, base.Add(5*time.Second), []byte(`{"rounds":["final"]}`),
	)
	championProjection := correctionWorkflowProjection(
		t, correctionWorkflowID(16), tournamentID, domain.ArtifactKindChampion,
		tournamentID, base.Add(6*time.Second), []byte(`{"participant":"first"}`),
	)
	graph, err := domain.NewRevisionGraph(
		[]domain.ProjectionRevision{
			gameProjection, scoreProjection, seriesProjection, standingsProjection,
			topFourProjection, bracketProjection, championProjection,
		},
		[]domain.RevisionDependency{
			{SourceRevisionID: gameProjection.Revision().ID(), DerivedRevisionID: scoreProjection.Revision().ID()},
			{SourceRevisionID: scoreProjection.Revision().ID(), DerivedRevisionID: seriesProjection.Revision().ID()},
			{SourceRevisionID: seriesProjection.Revision().ID(), DerivedRevisionID: standingsProjection.Revision().ID()},
			{SourceRevisionID: standingsProjection.Revision().ID(), DerivedRevisionID: topFourProjection.Revision().ID()},
			{SourceRevisionID: topFourProjection.Revision().ID(), DerivedRevisionID: bracketProjection.Revision().ID()},
			{SourceRevisionID: bracketProjection.Revision().ID(), DerivedRevisionID: championProjection.Revision().ID()},
		},
	)
	require.NoError(t, err)

	gameResultID := domain.OfficialResultRevisionID(correctionWorkflowID(20))
	resultCommandID := correctionWorkflowID(21)
	gameHead := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, GameID: gameID,
			Kind: resultusecase.OfficialResultSubjectGame,
		},
		ID: gameResultID, Ordinal: 1, CommandID: resultCommandID,
		Actor: domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome: resultusecase.OfficialResultOutcome{
			GameState: domain.GameStateCompleted, GameReason: domain.GameResultReasonOperatorForfeit,
			WinnerID: correctionWorkflowUUIDPointer(firstID),
		},
		SourceProjection: gameProjection.Revision(), RecordedAt: base.Add(3 * time.Second),
	}
	require.NoError(t, gameHead.Validate())

	attempt := resultusecase.SeriesScoreAttemptReference{
		SlotID: slotID, SlotPosition: 1, GameID: gameID, AttemptNo: 1,
		State: domain.GameStateCompleted, WinnerID: correctionWorkflowUUIDPointer(firstID),
		Reason: domain.GameResultReasonOperatorForfeit, CurrentGameResultRevisionID: gameResultID,
	}
	previousScoreID := domain.SeriesScoreRevisionID(correctionWorkflowID(22))
	scoreID := domain.SeriesScoreRevisionID(correctionWorkflowID(23))
	scoreHead := resultusecase.SeriesScoreRevisionHead{
		Scope: resultusecase.SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		ID:    scoreID, PreviousRevisionID: &previousScoreID, Ordinal: 2,
		Operation: resultusecase.SeriesScoreRevisionOperationAppendAttempt,
		CommandID: resultCommandID, Actor: domain.ResultActor{Kind: domain.ResultActorServer},
		CommandAttempt: &attempt, FirstParticipantID: firstID, SecondParticipantID: secondID,
		Format: domain.SeriesFormatBO1, Score: domain.SeriesScore{FirstParticipantWins: 1},
		Attempts:         []resultusecase.SeriesScoreAttemptReference{attempt},
		SourceProjection: scoreProjection.Revision(), RecordedAt: base.Add(3 * time.Second),
	}
	require.NoError(t, scoreHead.Validate())

	seriesResultID := domain.OfficialResultRevisionID(correctionWorkflowID(24))
	seriesHead := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID,
			Kind: resultusecase.OfficialResultSubjectSeries,
		},
		ID: seriesResultID, Ordinal: 1, CommandID: correctionWorkflowID(25),
		Actor: domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome: resultusecase.OfficialResultOutcome{
			SeriesState: domain.SeriesStateCompleted, SeriesReason: domain.SeriesResultReasonScoreComplete,
			WinnerID: correctionWorkflowUUIDPointer(firstID), ScoreRevisionID: &scoreID,
		},
		SourceProjection: seriesProjection.Revision(), RecordedAt: base.Add(4 * time.Second),
	}
	require.NoError(t, seriesHead.Validate())

	dag, err := resultprojection.BuildRevisionDAG(resultprojection.RevisionDAGInput{
		Graph: graph,
		Results: []resultprojection.OfficialResultProjectionInput{
			{TerminalSource: resultprojection.TerminalResultSourcePlayed, Result: gameHead, ResultProjection: gameProjection},
			{
				TerminalSource: resultprojection.TerminalResultSourcePlayed,
				Result:         seriesHead, ResultProjection: seriesProjection,
				Score: correctionWorkflowScorePointer(scoreHead), ScoreProjection: correctionWorkflowProjectionPointer(scoreProjection),
			},
		},
	})
	require.NoError(t, err)

	decisionPayload := []byte(`{"winner":"first"}`)
	authority := tournamentadmin.CorrectionWorkflowAuthority{
		RosterID: correctionWorkflowID(30), ProjectionRevisionID: correctionWorkflowID(31),
		ProjectionRevision: 7,
		Stage: correctionusecase.StageSnapshot{
			TournamentID: tournamentID, TournamentState: domain.TournamentStatePlayoffs,
			TournamentRevision: 8, Layout: correctionusecase.StageLayout{Mode: correctionusecase.StageModePlayoff},
			Swiss: correctionWorkflowSwissAuthority(seriesID, firstID, secondID),
		},
		Core: correctionusecase.Authority{
			TournamentState: domain.TournamentStatePlayoffs, TournamentRevision: 8,
			DAG: dag,
			Series: domain.Series{
				ID: seriesID, TournamentID: tournamentID, FirstParticipantID: firstID,
				SecondParticipantID: secondID, Format: domain.SeriesFormatBO1,
				State: domain.SeriesStateCompleted, Score: domain.SeriesScore{FirstParticipantWins: 1},
				WinnerID:               correctionWorkflowUUIDPointer(firstID),
				CurrentScoreRevisionID: &scoreID, CurrentResultRevisionID: &seriesResultID,
				Slots: []domain.GameSlot{{
					ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
					Attempts: []domain.Game{{
						ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateCompleted,
						ResultReason: domain.GameResultReasonOperatorForfeit,
						WinnerID:     correctionWorkflowUUIDPointer(firstID), ResultRevisionID: &gameResultID,
					}},
				}},
			},
			GameResult: gameHead, Score: scoreHead, SeriesResult: seriesHead,
			SeriesRevision: 5, AttemptRevision: 4,
			Readiness: correctionusecase.Readiness{
				TournamentID: tournamentID, OwnerID: seriesID, WaveID: correctionWorkflowID(32),
				WindowID: correctionWorkflowID(33), RevisionID: correctionWorkflowID(34), Revision: 2,
				State: correctionusecase.ReadinessOpen, ParticipantIDs: []uuid.UUID{firstID, secondID},
			},
			Decisions: []resultprojection.RecordedProjectionDecision{{
				ID: correctionWorkflowID(35), Sequence: 1,
				ProjectionRevisionID: seriesProjection.Revision().ID(), RecordedAt: base.Add(2 * time.Second),
				Payload: decisionPayload, PayloadDigest: sha256.Sum256(decisionPayload),
			}},
		},
	}

	command := tournamentadmin.CorrectionCommand{
		CommandScope: tournamentadmin.CommandScope{
			Operator:     tournamentadmin.OperatorIdentity{ActorID: correctionWorkflowID(40)},
			TournamentID: tournamentID, CommandID: correctionWorkflowID(41),
		},
		SeriesID: seriesID, GameID: gameID, ExpectedProjectionRevision: authority.ProjectionRevision,
		Confirmed: true, Reason: "operator_ruling", Explanation: "Verified referee ruling.",
		Fields: []string{"winner", "result_reason"},
		Patch: tournamentadmin.CorrectionPatch{
			State: domain.GameStateCompleted, Reason: domain.GameResultReasonSurrender,
			WinnerID: correctionWorkflowUUIDPointer(secondID),
		},
	}

	requestedAt := base.Add(time.Minute)
	for index, projection := range []domain.ProjectionRevision{
		gameProjection, scoreProjection, seriesProjection, standingsProjection,
		topFourProjection, bracketProjection, championProjection,
	} {
		revision := projection.Revision()
		intent := tournamentadmin.CorrectionProjectionIntent{
			ExpectedRevision: correctionWorkflowExpectation(revision),
			NextRevisionID:   correctionWorkflowID(50 + index), DecisionID: correctionWorkflowID(60 + index),
		}
		payload := correctionWorkflowPayload(t, command, intent)
		intent.PayloadDigest = sha256.Sum256(payload)
		command.ProjectionIntents = append(command.ProjectionIntents, intent)
	}
	return command, authority, requestedAt
}

func correctionWorkflowPausedGoldenStage(
	authority tournamentadmin.CorrectionWorkflowAuthority,
	changedAt time.Time,
) correctionusecase.StageSnapshot {
	firstID := authority.Core.Series.FirstParticipantID
	secondID := authority.Core.Series.SecondParticipantID
	groupID := correctionWorkflowID(90)
	groupRevisionID := domain.DerivedRevisionID(correctionWorkflowID(91))
	retainedAt := changedAt.Add(-time.Minute)
	attempt := domain.GoldenAttempt{
		ID: correctionWorkflowID(92), GroupID: groupID, GroupRevisionID: groupRevisionID,
		AttemptNo: 1, State: domain.GoldenAttemptStatePlanned,
		ParticipantIDs: []uuid.UUID{firstID, secondID}, RetainedAt: &retainedAt,
	}
	group := domain.GoldenGroupState{
		ID: groupID, TournamentID: authority.Core.Series.TournamentID,
		RevisionID:                 groupRevisionID,
		SourceProjectionRevisionID: authority.Core.GameResult.SourceProjection.ID(),
		PositionFrom:               1, PositionTo: 2, ParticipationEstablished: true,
		Members:  []domain.GoldenMember{{ParticipantID: firstID}, {ParticipantID: secondID}},
		Attempts: []domain.GoldenAttempt{attempt},
	}
	if _, err := domain.NewGoldenGroup(group); err != nil {
		panic(err)
	}
	return correctionusecase.StageSnapshot{
		TournamentID:       authority.Core.Series.TournamentID,
		TournamentState:    authority.Core.TournamentState,
		TournamentRevision: authority.Core.TournamentRevision,
		Layout: correctionusecase.StageLayout{
			Mode:         correctionusecase.StageModeGolden,
			GoldenGroups: []domain.GoldenGroupState{group},
			Paused: []correctionusecase.StagePauseExpectation{{
				TournamentID: authority.Core.Series.TournamentID,
				GroupID:      groupID, GroupRevisionID: groupRevisionID,
				SessionID: correctionWorkflowID(93), RevisionID: correctionWorkflowID(94), Revision: 1,
				State:         correctionusecase.StagePauseStatePaused,
				PayloadDigest: sha256.Sum256([]byte("paused Golden correction stage")),
			}},
		},
		Swiss: correctionWorkflowSwissAuthority(
			authority.Core.Series.ID,
			firstID,
			secondID,
		),
	}
}

// correctionWorkflowSwissAuthority models the normalized, complete Swiss
// ledger which production code locks before deriving a server-owned rollback.
// The corrected series is deliberately present once as a complementary pair.
func correctionWorkflowSwissAuthority(
	seriesID uuid.UUID,
	firstID uuid.UUID,
	secondID uuid.UUID,
) correctionusecase.StageSwissAuthority {
	thirdID := correctionWorkflowID(140)
	fourthID := correctionWorkflowID(141)
	participants := []uuid.UUID{firstID, secondID, thirdID, fourthID}
	seeds := map[uuid.UUID]int{firstID: 1, secondID: 2, thirdID: 3, fourthID: 4}
	ledger := make([]resultprojection.CanonicalSwissPointLedgerEntry, 0, 12)
	appendResult := func(round int, currentSeriesID, first, second, winner uuid.UUID) {
		resultID := correctionWorkflowID(150 + len(ledger))
		firstPoints, secondPoints := 0, 0
		if winner == first {
			firstPoints = swissusecase.SeriesWinPoints
		} else {
			secondPoints = swissusecase.SeriesWinPoints
		}
		ledger = append(ledger,
			resultprojection.CanonicalSwissPointLedgerEntry{
				RoundID: correctionWorkflowID(160 + round), RoundRevisionID: correctionWorkflowID(170 + round),
				RoundNumber: round, SourceKind: swissusecase.PointSourceSeries, SourceSeriesID: currentSeriesID,
				SeriesResultRevisionID: resultID, ResultLabel: swissusecase.SeriesResultPlayed,
				ParticipantID: first, OpponentID: correctionWorkflowUUIDPointer(second),
				Points: firstPoints, StableSeed: seeds[first],
			},
			resultprojection.CanonicalSwissPointLedgerEntry{
				RoundID: correctionWorkflowID(160 + round), RoundRevisionID: correctionWorkflowID(170 + round),
				RoundNumber: round, SourceKind: swissusecase.PointSourceSeries, SourceSeriesID: currentSeriesID,
				SeriesResultRevisionID: resultID, ResultLabel: swissusecase.SeriesResultPlayed,
				ParticipantID: second, OpponentID: correctionWorkflowUUIDPointer(first),
				Points: secondPoints, StableSeed: seeds[second],
			},
		)
	}
	appendResult(1, seriesID, firstID, secondID, firstID)
	appendResult(1, correctionWorkflowID(180), thirdID, fourthID, thirdID)
	appendResult(2, correctionWorkflowID(181), firstID, thirdID, firstID)
	appendResult(2, correctionWorkflowID(182), secondID, fourthID, secondID)
	appendResult(3, correctionWorkflowID(183), firstID, fourthID, firstID)
	appendResult(3, correctionWorkflowID(184), secondID, thirdID, secondID)

	canonical := make([]resultprojection.CanonicalSwissParticipant, len(participants))
	for index, participantID := range participants {
		canonical[index] = resultprojection.CanonicalSwissParticipant{ID: participantID, StableSeed: seeds[participantID]}
	}
	return correctionusecase.StageSwissAuthority{Participants: canonical, Ledger: ledger, Complete: true}
}

func correctionWorkflowExpectation(revision domain.DerivedRevision) tournamentadmin.ProjectionRevisionExpectation {
	var previous *uuid.UUID
	if value := revision.PreviousRevisionID(); value != nil {
		id := value.UUID()
		previous = &id
	}
	return tournamentadmin.ProjectionRevisionExpectation{
		ID: revision.ID().UUID(), TournamentID: revision.TournamentID(),
		ArtifactKind: string(revision.Artifact().Kind), ArtifactID: revision.Artifact().EntityID,
		RevisionNo: revision.RevisionNo(), PreviousRevisionID: previous,
		PayloadDigest: revision.PayloadDigest(), CreatedAt: revision.CreatedAt(),
	}
}

func correctionWorkflowPayload(
	t *testing.T,
	command tournamentadmin.CorrectionCommand,
	intent tournamentadmin.CorrectionProjectionIntent,
) []byte {
	t.Helper()
	fields := append([]string(nil), command.Fields...)
	sort.Strings(fields)
	type artifactDocument struct {
		Kind                     string     `json:"kind"`
		ID                       uuid.UUID  `json:"id"`
		ExpectedRevisionID       uuid.UUID  `json:"expected_revision_id"`
		ExpectedRevisionNo       int        `json:"expected_revision_no"`
		ExpectedPreviousRevision *uuid.UUID `json:"expected_previous_revision_id"`
		ExpectedPayloadDigest    string     `json:"expected_payload_digest"`
		NextRevisionID           uuid.UUID  `json:"next_revision_id"`
		DecisionID               uuid.UUID  `json:"decision_id"`
	}
	type patchDocument struct {
		State          domain.GameState        `json:"state"`
		Reason         domain.GameResultReason `json:"reason"`
		WinnerID       *uuid.UUID              `json:"winner_id"`
		SolvedAt       *time.Time              `json:"solved_at"`
		SubmissionID   *uuid.UUID              `json:"submission_id"`
		EvidenceDigest string                  `json:"evidence_digest"`
	}
	document := struct {
		Schema       string           `json:"schema"`
		TournamentID uuid.UUID        `json:"tournament_id"`
		SeriesID     uuid.UUID        `json:"series_id"`
		GameID       uuid.UUID        `json:"game_id"`
		CommandID    uuid.UUID        `json:"command_id"`
		Reason       string           `json:"reason"`
		Explanation  string           `json:"explanation"`
		Fields       []string         `json:"fields"`
		Artifact     artifactDocument `json:"artifact"`
		Patch        patchDocument    `json:"patch"`
	}{
		Schema: "tournament-correction-projection-v1", TournamentID: command.TournamentID,
		SeriesID: command.SeriesID, GameID: command.GameID, CommandID: command.CommandID,
		Reason: command.Reason, Explanation: command.Explanation, Fields: fields,
		Artifact: artifactDocument{
			Kind: intent.ExpectedRevision.ArtifactKind, ID: intent.ExpectedRevision.ArtifactID,
			ExpectedRevisionID:       intent.ExpectedRevision.ID,
			ExpectedRevisionNo:       intent.ExpectedRevision.RevisionNo,
			ExpectedPreviousRevision: intent.ExpectedRevision.PreviousRevisionID,
			ExpectedPayloadDigest:    hex.EncodeToString(intent.ExpectedRevision.PayloadDigest[:]),
			NextRevisionID:           intent.NextRevisionID, DecisionID: intent.DecisionID,
		},
		Patch: patchDocument{
			State: command.Patch.State, Reason: command.Patch.Reason,
			WinnerID: command.Patch.WinnerID, SolvedAt: command.Patch.SolvedAt,
			SubmissionID:   command.Patch.SubmissionID,
			EvidenceDigest: hex.EncodeToString(command.Patch.EvidenceDigest[:]),
		},
	}
	payload, err := json.Marshal(document)
	require.NoError(t, err)
	return payload
}

func correctionWorkflowRequestDigest(t *testing.T, command tournamentadmin.CorrectionCommand) [sha256.Size]byte {
	t.Helper()
	canonical := command
	canonical.Fields = append([]string(nil), command.Fields...)
	sort.Strings(canonical.Fields)
	canonical.ProjectionIntents = append([]tournamentadmin.CorrectionProjectionIntent(nil), command.ProjectionIntents...)
	sort.Slice(canonical.ProjectionIntents, func(i, j int) bool {
		return canonical.ProjectionIntents[i].ExpectedRevision.ID.String() <
			canonical.ProjectionIntents[j].ExpectedRevision.ID.String()
	})
	canonical.UnlockIntents = append([]tournamentadmin.CorrectionUnlockIntent(nil), command.UnlockIntents...)
	sort.Slice(canonical.UnlockIntents, func(i, j int) bool {
		return canonical.UnlockIntents[i].ReservationID.String() < canonical.UnlockIntents[j].ReservationID.String()
	})
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	payload, err := json.Marshal(canonical)
	require.NoError(t, err)
	return sha256.Sum256(payload)
}

func correctionWorkflowProjection(
	t *testing.T,
	id uuid.UUID,
	tournamentID uuid.UUID,
	kind domain.ArtifactKind,
	entityID uuid.UUID,
	createdAt time.Time,
	payload []byte,
) domain.ProjectionRevision {
	t.Helper()
	projection, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(id), tournamentID,
		domain.ArtifactRef{Kind: kind, EntityID: entityID}, 1, nil, createdAt, payload,
	)
	require.NoError(t, err)
	return projection
}

func correctionWorkflowScorePointer(
	value resultusecase.SeriesScoreRevisionHead,
) *resultusecase.SeriesScoreRevisionHead {
	clone := value.Clone()
	return &clone
}

func correctionWorkflowProjectionPointer(value domain.ProjectionRevision) *domain.ProjectionRevision {
	revision := value.Revision()
	clone, err := domain.NewProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), value.Payload(),
	)
	if err != nil {
		panic(err)
	}
	return &clone
}

func correctionWorkflowID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("77000000-0000-0000-0000-%012x", value))
}

func correctionWorkflowUUIDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}
