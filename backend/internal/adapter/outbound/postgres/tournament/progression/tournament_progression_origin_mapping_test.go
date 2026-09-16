package progression

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestProgressionReceiptOrdinaryHeadHydration(t *testing.T) {
	input := progressionTiedSwissInput(t)
	evidence := input.Rounds[0].Series[0]
	official := evidence.OfficialResult()
	authority := progressionReceiptChainAuthority(input.TournamentID, uuid.New(), input.RevisionID.UUID(), 1)
	result := official.Result
	score := *official.Score
	resultNode, err := domain.NewProjectionRevision(domain.DerivedRevisionID(result.ID.UUID()), input.TournamentID, official.ResultProjection.Revision().Artifact(), result.Ordinal, nil, result.RecordedAt, official.ResultProjection.Payload())
	require.NoError(t, err)
	previous := domain.DerivedRevisionID(score.PreviousRevisionID.UUID())
	scoreNode, err := domain.NewProjectionRevision(domain.DerivedRevisionID(score.ID.UUID()), input.TournamentID, official.ScoreProjection.Revision().Artifact(), score.Ordinal, &previous, score.RecordedAt, official.ScoreProjection.Payload())
	require.NoError(t, err)
	physical := make([]sqlc.LockTournamentProgressionFinalSwissReceiptProjectionNodesRow, 0, 2)
	logical := make([]sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow, 0, 2)
	for _, node := range []domain.ProjectionRevision{resultNode, scoreNode} {
		revision := node.Revision()
		digest := revision.PayloadDigest()
		var previous uuid.NullUUID
		if revision.PreviousRevisionID() != nil {
			previous = nullableUUIDValue(revision.PreviousRevisionID().UUID())
		}
		authorityID := uuid.New()
		physical = append(physical, sqlc.LockTournamentProgressionFinalSwissReceiptProjectionNodesRow{
			ProjectionRevisionID: input.RevisionID.UUID(), ID: revision.ID().UUID(), AuthorityID: authorityID,
			ArtifactKind: string(revision.Artifact().Kind), EntityID: revision.Artifact().EntityID,
			RevisionNumber: int64(revision.RevisionNo()), PreviousNodeID: previous, Payload: node.Payload(), PayloadDigest: digest[:], CreatedAt: tstz(revision.CreatedAt()),
		})
		logical = append(logical, sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow{
			ReceiptProjectionRevisionID: input.RevisionID.UUID(), HeadKind: string(revision.Artifact().Kind),
			EntityID: revision.Artifact().EntityID, SourceID: revision.ID().UUID(), NodeID: revision.ID().UUID(),
			AuthorityID: authorityID, SourceKind: "result_commit", ArtifactKind: string(revision.Artifact().Kind),
			RevisionNumber: int64(revision.RevisionNo()), PreviousNodeID: previous, Payload: node.Payload(), PayloadDigest: digest[:], NodeCreatedAt: tstz(revision.CreatedAt()),
		})
	}
	nodes, err := progressionReceiptNodeIndex(authority, physical, logical)
	require.NoError(t, err)
	row := sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow{
		SeriesID: evidence.Series().ID, SeriesResultRevisionID: result.ID.UUID(), SeriesResultNodeID: result.ID.UUID(),
		SeriesResultRevisionNumber: int64(result.Ordinal), SeriesResultEventID: uuid.New(), SeriesResultCommandID: result.CommandID,
		SeriesResultActorKind: string(result.Actor.Kind), SeriesResultState: string(result.Outcome.SeriesState),
		SeriesResultReason: string(result.Outcome.SeriesReason), SeriesWinnerID: nullableUUIDValue(*result.Outcome.WinnerID),
		SeriesResultCreatedAt: tstz(result.RecordedAt), SeriesResultOccurredAt: tstz(result.RecordedAt),
		ScoreRevisionID: score.ID.UUID(), ScoreNodeID: score.ID.UUID(), ScoreRevisionNumber: int64(score.Ordinal),
		ScorePreviousRevisionID: nullableUUIDValue(score.PreviousRevisionID.UUID()), ScoreResultEventID: nullableUUIDValue(uuid.New()),
		ScoreCommandID: score.CommandID, ScoreActorKind: string(score.Actor.Kind), ScoreCreatedAt: tstz(score.RecordedAt), ScoreOccurredAt: tstz(score.RecordedAt),
		ScoreOperation: string(score.Operation), ScoreCommandAttemptID: nullableUUIDValue(score.CommandAttempt.GameID),
		FirstParticipantID: score.FirstParticipantID, SecondParticipantID: score.SecondParticipantID, SeriesFormat: string(score.Format),
	}
	t.Run("official", func(t *testing.T) {
		head, err := progressionReceiptSeriesResultHead(authority, row, nodes[progressionReceiptNodeKey{input.RevisionID.UUID(), result.ID.UUID()}], nil)
		require.NoError(t, err)
		require.True(t, head.HasOrdinarySourceIdentity())
		require.NoError(t, head.Clone().Validate())
	})
	t.Run("score", func(t *testing.T) {
		attempt := score.Attempts[0]
		attemptRows := []sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAttemptsRow{{
			ProjectionRevisionID: input.RevisionID.UUID(), SeriesID: row.SeriesID, ScoreRevisionID: score.ID.UUID(), Position: 1,
			SlotID: attempt.SlotID, SlotPosition: int16(attempt.SlotPosition), GameAttemptID: attempt.GameID,
			AttemptNumber: int32(attempt.AttemptNo), GameResultRevisionID: attempt.CurrentGameResultRevisionID.UUID(),
			ResultEventID: uuid.New(), OccurredAt: tstz(score.RecordedAt), CreatedAt: tstz(score.RecordedAt),
			ResultState: string(attempt.State), ResultReason: string(attempt.Reason), WinnerID: nullableUUIDValue(*attempt.WinnerID),
		}}
		head, err := progressionReceiptScoreHead(authority, sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{ProjectionRevisionID: input.RevisionID.UUID()}, row, evidence.Series(), nodes[progressionReceiptNodeKey{input.RevisionID.UUID(), score.ID.UUID()}], attemptRows, nil, nil)
		require.NoError(t, err)
		require.True(t, head.HasOrdinarySourceIdentity())
		require.NoError(t, head.Clone().Validate())
	})
	for name, change := range map[string]func(*sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow){
		"origin": func(r *sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow) {
			r.SourceKind = "correction_commit"
		},
		"source": func(r *sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow) { r.SourceID = uuid.New() },
		"kind": func(r *sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow) {
			r.HeadKind = "series_score"
		},
		"authority": func(r *sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow) {
			r.AuthorityID = uuid.New()
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := append([]sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow(nil), logical...)
			change(&changed[0])
			_, err := progressionReceiptNodeIndex(authority, physical, changed)
			require.ErrorIs(t, err, domain.ErrConflict)
		})
	}
}
