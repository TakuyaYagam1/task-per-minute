package playoff

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestPlayoffTerminalPostgresImplementsTerminalRepository(t *testing.T) {
	t.Parallel()

	var repository playoff.TerminalRepository = NewPlayoffTerminalPostgres(nil, nil, nil)
	require.NotNil(t, repository)
}

func TestFinalNextGameUsesOnlyReservedIdentities(t *testing.T) {
	t.Parallel()

	ids, err := playoff.FinalStageIdentity(uuid.New())
	require.NoError(t, err)

	second, err := finalNextGame(ids, 2, domain.CategoryWeb)
	require.NoError(t, err)
	require.Equal(t, ids.SecondSlotID, second.SlotID)
	require.Equal(t, ids.SecondGameID, second.GameID)
	require.Equal(t, ids.SecondWaveID, second.WaveID)

	third, err := finalNextGame(ids, 3, domain.CategoryCrypto)
	require.NoError(t, err)
	require.Equal(t, ids.ThirdSlotID, third.SlotID)
	require.Equal(t, ids.ThirdGameID, third.GameID)
	require.Equal(t, ids.ThirdWaveID, third.WaveID)

	_, err = finalNextGame(ids, 1, domain.CategoryWeb)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestTerminalSemifinalSeriesRejectsStaleHeads(t *testing.T) {
	t.Parallel()

	currentScore := uuid.New()
	currentResult := uuid.New()
	staleScore := uuid.New()
	scoreRevision := int64(2)
	resultRevision := int64(3)
	_, err := terminalSemifinalSeries(sqlc.LockPostseasonSemifinalAuthorityRow{
		State:                   string(domain.SeriesStateCompleted),
		CurrentScoreRevisionID:  uuid.NullUUID{UUID: currentScore, Valid: true},
		CurrentResultRevisionID: uuid.NullUUID{UUID: currentResult, Valid: true},
		ScoreHeadRevisionID:     staleScore,
		ResultHeadRevisionID:    currentResult,
		ScoreHeadRevision:       scoreRevision,
		ResultHeadRevision:      resultRevision,
	})
	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestSemifinalCompletionTimeRequiresExactCurrentHead(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	at := time.Date(2026, 9, 8, 12, 0, 30, 0, time.UTC)
	row := sqlc.LockPostseasonSemifinalAuthorityRow{
		CompletedResultRevisionID: id, ResultHeadRevisionID: id,
		CurrentResultRevisionID: nullableUUIDValue(id), CompletedAt: tstz(at),
	}
	got, err := semifinalCompletionTime(row)
	require.NoError(t, err)
	require.Equal(t, at, got)
	for _, field := range []string{"completion revision", "head revision", "Series pointer", "time"} {
		wrong := row
		switch field {
		case "completion revision":
			wrong.CompletedResultRevisionID = uuid.New()
		case "head revision":
			wrong.ResultHeadRevisionID = uuid.New()
		case "Series pointer":
			wrong.CurrentResultRevisionID = nullableUUIDValue(uuid.New())
		case "time":
			wrong.CompletedAt.Valid = false
		}
		_, err := semifinalCompletionTime(wrong)
		require.ErrorIs(t, err, domain.ErrConflict, field)
	}
}

func TestFinalInitializationMatchBindsCompletedDraftRevision(t *testing.T) {
	t.Parallel()

	stageCommandID := uuid.New()
	tournamentID := uuid.New()
	rosterID := uuid.New()
	seriesID := uuid.New()
	draftID := uuid.New()
	draftRevisionID := uuid.New()
	initialScoreID := domain.SeriesScoreRevisionID(uuid.New())
	slotID := uuid.New()
	gameID := uuid.New()
	waveID := uuid.New()
	waveRevisionID := domain.WaveRevisionID(uuid.New())
	assignmentID := uuid.New()
	stage := sqlc.LockPostseasonFinalStageRow{
		CommandID:              stageCommandID,
		TournamentID:           tournamentID,
		RosterID:               rosterID,
		FinalSeriesID:          seriesID,
		DraftID:                draftID,
		CurrentDraftRevisionID: draftRevisionID,
	}
	plan := playoff.FinalInitialPlan{
		RosterID: rosterID,
		Execution: seriesdomain.Execution{Series: domain.Series{
			ID: seriesID, TournamentID: tournamentID,
			Slots: []domain.GameSlot{{ID: slotID}},
		}},
		InitialScoreRevisionID: initialScoreID,
		CurrentWave:            domain.Wave{ID: waveID, RevisionID: waveRevisionID},
		Binding:                playoff.FinalGameBinding{GameID: gameID, AssignmentID: assignmentID},
	}
	row := sqlc.TournamentStagePlayoffFinalInitialization{
		CommandID:                stageCommandID,
		TournamentID:             tournamentID,
		RosterID:                 rosterID,
		FinalSeriesID:            seriesID,
		DraftID:                  draftID,
		CompletedDraftRevisionID: draftRevisionID,
		InitialScoreRevisionID:   initialScoreID.UUID(),
		FirstSlotID:              slotID,
		FirstGameID:              gameID,
		FirstWaveID:              waveID,
		FirstWaveRevisionID:      waveRevisionID.UUID(),
		FirstAssignmentID:        assignmentID,
	}
	require.True(t, finalInitializationMatchesPlan(row, plan, stage))
	row.CompletedDraftRevisionID = uuid.New()
	require.False(t, finalInitializationMatchesPlan(row, plan, stage))
}

func TestFinalPublicationArtifactsCloneExactBaseAndAddChampion(t *testing.T) {
	t.Parallel()

	resultID := domain.OfficialResultRevisionID(uuid.New())
	ids, err := playoff.FinalPublicationIdentity(resultID)
	require.NoError(t, err)
	tournamentID := uuid.New()
	rosterID := uuid.New()
	seriesID := uuid.New()
	baseRevisionID := uuid.New()
	participants := [4]uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	semifinalSeries := [2]uuid.UUID{uuid.New(), uuid.New()}
	winnerID := participants[0]

	standings := terminalPublicationSourceArtifact(
		domain.ArtifactKindStandings,
		`{"entries":[{"participant_id":"one"}]}`,
		[]sqlc.ProjectionArtifactMember{{
			ParticipantID: uuid.New(),
			Position:      1,
			Score:         pgtype.Numeric{Int: big.NewInt(1750), Exp: -3, Valid: true},
		}},
		baseRevisionID,
		tournamentID,
		rosterID,
	)
	bracket := terminalPublicationSourceArtifact(
		domain.ArtifactKindBracket,
		`{"rounds":[{"position":1}]}`,
		[]sqlc.ProjectionArtifactMember{{ParticipantID: participants[0], Position: 1}},
		baseRevisionID,
		tournamentID,
		rosterID,
	)
	topFourMembers := make([]sqlc.ProjectionArtifactMember, 4)
	for index := range topFourMembers {
		topFourMembers[index] = sqlc.ProjectionArtifactMember{
			ParticipantID: participants[index],
			Position:      int32(index + 1),
		}
	}
	topFour := terminalPublicationSourceArtifact(
		domain.ArtifactKindTopFour,
		`{"participants":["a","b","c","d"]}`,
		topFourMembers,
		baseRevisionID,
		tournamentID,
		rosterID,
	)
	stage := sqlc.LockPostseasonFinalStageRow{
		TournamentID:            tournamentID,
		RosterID:                rosterID,
		FinalSeriesID:           seriesID,
		FirstParticipantID:      participants[0],
		SecondParticipantID:     participants[2],
		CurrentResultRevisionID: uuid.NullUUID{UUID: resultID.UUID(), Valid: true},
	}
	aggregate := sqlc.LockFinalProjectionAggregateRow{
		SeriesFormat: string(domain.SeriesFormatBO3), SeriesState: string(domain.SeriesStateCompleted),
		FirstParticipantID: participants[0], SecondParticipantID: participants[2],
		FirstParticipantWins: 2, WinnerID: uuid.NullUUID{UUID: winnerID, Valid: true},
	}
	semifinals := playoff.SemifinalAdvancementAuthority{
		TournamentID: tournamentID,
		Semifinals: []playoff.SemifinalMatch{
			{Position: 1, Series: domain.Series{
				ID: semifinalSeries[0], TournamentID: tournamentID,
				FirstParticipantID: participants[0], SecondParticipantID: participants[1],
				Format: domain.SeriesFormatBO1, State: domain.SeriesStateLocked,
			}},
			{Position: 2, Series: domain.Series{
				ID: semifinalSeries[1], TournamentID: tournamentID,
				FirstParticipantID: participants[2], SecondParticipantID: participants[3],
				Format: domain.SeriesFormatBO1, State: domain.SeriesStateLocked,
			}},
		},
	}
	advancement := []playoff.SemifinalAdvancementResult{
		{Position: 1, SeriesID: semifinalSeries[0], WinnerID: participants[0], LoserID: participants[1]},
		{Position: 2, SeriesID: semifinalSeries[1], WinnerID: participants[2], LoserID: participants[3]},
	}
	record := ProjectionRecord{
		Revision:  sqlc.ProjectionRevision{ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID, RevisionNumber: 2},
		Artifacts: []ProjectionArtifactRecord{standings, bracket, topFour},
	}
	memberships := make([]sqlc.LockFinalPublicationArtifactMembershipRow, len(record.Artifacts))
	for i, item := range record.Artifacts {
		memberships[i] = sqlc.LockFinalPublicationArtifactMembershipRow{
			RevisionID: record.Revision.ID, TournamentID: tournamentID, RosterID: rosterID,
			ArtifactID: item.Artifact.ID, ArtifactKind: item.Artifact.ArtifactKind, ChangeKind: "reused",
			ProducedByRevisionID: baseRevisionID, ProducerRevision: 1, PayloadDigest: item.Artifact.PayloadDigest,
		}
	}
	artifacts, err := finalPublicationArtifacts(ids, record, stage, aggregate, semifinals, advancement, memberships)
	require.NoError(t, err)
	require.Len(t, artifacts, 4)
	require.Equal(t, ids.StandingsArtifactID, artifacts[0].ID)
	require.Equal(t, ids.BracketArtifactID, artifacts[1].ID)
	require.Equal(t, ids.TopFourArtifactID, artifacts[2].ID)
	require.Equal(t, ids.ChampionArtifactID, artifacts[3].ID)
	require.Equal(t, winnerID, artifacts[3].Members[0].ParticipantID)
	require.Equal(t, projection.DependencyOfficialResult, artifacts[0].Dependencies[0].Kind)
	require.Equal(t, projection.DependencyArtifact, artifacts[3].Dependencies[0].Kind)
	require.Equal(t, projection.DependencyOfficialResult, artifacts[3].Dependencies[1].Kind)
	var finalBracket finalBracketPayloadDocument
	require.NoError(t, json.Unmarshal(artifacts[1].Payload, &finalBracket))
	require.Len(t, finalBracket.Rounds, 3)
	require.Equal(t, "final", finalBracket.Rounds[2].Stage)
	require.Equal(t, domain.SeriesStateCompleted, finalBracket.Rounds[2].State)
	for _, field := range []string{"missing", "duplicate", "revision", "tournament", "roster", "kind", "artifact", "digest", "producer", "future producer", "change kind"} {
		t.Run(field, func(t *testing.T) {
			changed := append([]sqlc.LockFinalPublicationArtifactMembershipRow(nil), memberships...)
			switch field {
			case "missing":
				changed = changed[:2]
			case "duplicate":
				changed[1] = changed[0]
			case "revision":
				changed[0].RevisionID = uuid.New()
			case "tournament":
				changed[0].TournamentID = uuid.New()
			case "roster":
				changed[0].RosterID = uuid.New()
			case "kind":
				changed[0].ArtifactKind = string(domain.ArtifactKindBracket)
			case "artifact":
				changed[0].ArtifactID = uuid.New()
			case "digest":
				changed[0].PayloadDigest = make([]byte, sha256.Size)
			case "producer":
				changed[0].ProducedByRevisionID = uuid.New()
			case "future producer":
				changed[0].ProducerRevision = 3
			case "change kind":
				changed[0].ChangeKind = "produced"
			}
			_, err := finalPublicationArtifacts(ids, record, stage, aggregate, semifinals, advancement, changed)
			require.ErrorIs(t, err, domain.ErrConflict)
		})
	}
}

func TestPlayoffTerminalSQLUsesLockedStageHeadsAndPlannedWaves(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "..", "..", "..", "db", "queries", "playoff_terminal.sql")
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(source)

	for _, fragment := range []string{
		"-- name: LockPostseasonSemifinalAuthority :many",
		"evidence.bracket_node_id",
		"INNER JOIN series_score_heads AS score_head",
		"INNER JOIN official_result_heads AS result_head",
		"FOR UPDATE OF evidence, semifinal, series, score_head, result_head",
		"-- name: LockPostseasonFinalStage :one",
		"-- name: LockPostseasonFinalScoreHistory :many",
		"-- name: CreatePostseasonPlannedWave :exec",
		"'planned'",
		"-- name: ActivatePostseasonFinalSeriesCAS :one",
		"-- name: CreatePostseasonFinalProgression :exec",
	} {
		require.Contains(t, text, fragment)
	}
	require.NotContains(t, text, "CreatePostseasonActiveWave")
}

func terminalPublicationSourceArtifact(
	kind domain.ArtifactKind,
	payload string,
	members []sqlc.ProjectionArtifactMember,
	revisionID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
) ProjectionArtifactRecord {
	digest := sha256.Sum256([]byte(payload))
	return ProjectionArtifactRecord{
		Artifact: sqlc.ProjectionArtifact{
			ID:                   uuid.New(),
			TournamentID:         tournamentID,
			RosterID:             rosterID,
			ProducedByRevisionID: revisionID,
			ArtifactKind:         string(kind),
			ArtifactKey:          string(kind),
			Payload:              []byte(payload),
			PayloadDigest:        digest[:],
		},
		Members: members,
	}
}

func TestTerminalScoreMilliAcceptsPostgresNumericNormalization(t *testing.T) {
	for _, value := range []pgtype.Numeric{
		{Int: big.NewInt(0), Exp: 0, Valid: true},
		{Int: big.NewInt(0), Exp: -3, Valid: true},
	} {
		got, err := terminalScoreMilli(value)
		require.NoError(t, err)
		require.Zero(t, *got)
	}
	for _, value := range []pgtype.Numeric{
		{}, {Int: big.NewInt(-1), Exp: -3, Valid: true},
		{Int: big.NewInt(12), Exp: -4, Valid: true},
		{Int: big.NewInt(0), NaN: true, Valid: true},
		{Int: big.NewInt(0), InfinityModifier: pgtype.Infinity, Valid: true},
	} {
		_, err := terminalScoreMilli(value)
		require.ErrorIs(t, err, domain.ErrConflict)
	}
}
