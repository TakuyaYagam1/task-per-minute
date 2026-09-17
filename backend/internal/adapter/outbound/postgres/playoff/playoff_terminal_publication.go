package playoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/publication"
)

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (repository *PlayoffTerminalPostgres) finalPublication(
	ctx context.Context,
	stage sqlc.LockPostseasonFinalStageRow,
	aggregate sqlc.LockFinalProjectionAggregateRow,
	progression playoff.FinalProgressionCommand,
	semifinalBracket playoff.SemifinalAdvancementAuthority,
	advancement []playoff.SemifinalAdvancementResult,
) (*projection.FinalPublication, error) {
	if progression.Progression.Game.ResultRevisionID == nil ||
		progression.Progression.TerminalResultRevisionID == nil || progression.ChampionRevisionID.IsZero() ||
		aggregate.TournamentState != string(domain.TournamentStatePlayoffs) &&
			aggregate.TournamentState != string(domain.TournamentStateCompleted) ||
		aggregate.SeriesState != string(domain.SeriesStateCompleted) || !aggregate.WinnerID.Valid ||
		!aggregate.CurrentResultRevisionID.Valid ||
		aggregate.CurrentResultRevisionID.UUID != progression.Progression.TerminalResultRevisionID.UUID() {
		return nil, domain.ErrConflict
	}
	ids, err := playoff.FinalPublicationIdentity(*progression.Progression.Game.ResultRevisionID)
	if err != nil || ids.ChampionRevisionID != progression.ChampionRevisionID {
		if err != nil {
			return nil, err
		}
		return nil, domain.ErrConflict
	}
	querier := repository.tx.Querier(ctx)
	attempts, err := querier.LockFinalProjectionAttempts(ctx, sqlc.LockFinalProjectionAttemptsParams{
		SeriesID: stage.FinalSeriesID,
		RosterID: stage.RosterID,
	})
	if err != nil {
		return nil, err
	}
	var currentAttempt sqlc.LockFinalProjectionAttemptsRow
	foundAttempt := false
	for _, attempt := range attempts {
		if attempt.AttemptID != progression.Progression.Game.ID {
			continue
		}
		if foundAttempt || attempt.HeadRevisionID != progression.Progression.Game.ResultRevisionID.UUID() ||
			attempt.ResultState != string(domain.GameStateCompleted) || !attempt.ResultWinnerID.Valid ||
			attempt.ResultWinnerID.UUID != aggregate.WinnerID.UUID {
			return nil, domain.ErrConflict
		}
		currentAttempt = attempt
		foundAttempt = true
	}
	if !foundAttempt {
		return nil, domain.ErrConflict
	}
	scoreHead, err := querier.LockFinalProjectionScoreHead(ctx, sqlc.LockFinalProjectionScoreHeadParams{
		SeriesID: stage.FinalSeriesID,
		RosterID: stage.RosterID,
	})
	if err != nil || scoreHead.CurrentRevisionID != progression.Progression.ScoreRevision.ID.UUID() ||
		scoreHead.HeadRevision < 1 || !scoreHead.ResultEventID.Valid {
		if err != nil {
			return nil, err
		}
		return nil, domain.ErrConflict
	}
	seriesHead, err := querier.LockFinalProjectionSeriesResultHead(ctx, sqlc.LockFinalProjectionSeriesResultHeadParams{
		SeriesID: stage.FinalSeriesID,
		RosterID: stage.RosterID,
	})
	if err != nil || seriesHead.CurrentRevisionID != progression.Progression.TerminalResultRevisionID.UUID() ||
		seriesHead.HeadRevision < 1 || seriesHead.ResultState != string(domain.SeriesStateCompleted) ||
		!seriesHead.WinnerID.Valid || seriesHead.WinnerID.UUID != aggregate.WinnerID.UUID {
		if err != nil {
			return nil, err
		}
		return nil, domain.ErrConflict
	}
	commits, err := querier.LockFinalProjectionResultCommits(ctx, sqlc.LockFinalProjectionResultCommitsParams{
		TournamentID: stage.TournamentID,
		RosterID:     stage.RosterID,
		SeriesID:     stage.FinalSeriesID,
	})
	if err != nil {
		return nil, err
	}
	var terminalCommit sqlc.LockFinalProjectionResultCommitsRow
	foundCommit := false
	for _, commit := range commits {
		if commit.AttemptID != currentAttempt.AttemptID ||
			commit.GameResultRevisionID != progression.Progression.Game.ResultRevisionID.UUID() ||
			commit.SeriesScoreRevisionID != progression.Progression.ScoreRevision.ID.UUID() ||
			!commit.SeriesResultRevisionID.Valid ||
			commit.SeriesResultRevisionID.UUID != progression.Progression.TerminalResultRevisionID.UUID() {
			continue
		}
		if foundCommit || len(commit.PayloadDigest) != sha256.Size {
			return nil, domain.ErrConflict
		}
		terminalCommit = commit
		foundCommit = true
	}
	if !foundCommit {
		return nil, domain.ErrConflict
	}
	if _, err = querier.LockProjectionRevisionSet(ctx, sqlc.LockProjectionRevisionSetParams{
		TournamentID: stage.TournamentID,
		RosterID:     stage.RosterID,
	}); err != nil {
		return nil, err
	}
	currentProjection, err := querier.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: stage.TournamentID,
		RosterID:     stage.RosterID,
	})
	if err != nil || currentProjection.RevisionNumber < 1 {
		if err != nil {
			return nil, err
		}
		return nil, domain.ErrConflict
	}
	expectedTournamentRevision := aggregate.TournamentRevision
	expectedProjectionRevision := currentProjection.RevisionNumber
	baseProjectionID := currentProjection.ID
	if currentProjection.ID == ids.ProjectionRevisionID {
		if aggregate.TournamentState != string(domain.TournamentStateCompleted) ||
			aggregate.TournamentRevision < 2 || currentProjection.RevisionNumber < 2 ||
			!currentProjection.PreviousRevisionID.Valid {
			return nil, domain.ErrConflict
		}
		expectedTournamentRevision--
		expectedProjectionRevision--
		baseProjectionID = currentProjection.PreviousRevisionID.UUID
	}
	record, err := loadProjectionRecord(ctx, querier, ProjectionScope{
		TournamentID: stage.TournamentID,
		RosterID:     stage.RosterID,
	}, baseProjectionID)
	if err != nil {
		return nil, err
	}
	memberships, err := querier.LockFinalPublicationArtifactMembership(ctx, sqlc.LockFinalPublicationArtifactMembershipParams{
		RevisionID: record.Revision.ID, TournamentID: stage.TournamentID, RosterID: stage.RosterID,
	})
	if err != nil {
		return nil, err
	}
	artifacts, err := finalPublicationArtifacts(
		ids,
		*record,
		stage,
		aggregate,
		semifinalBracket,
		advancement,
		memberships,
	)
	if err != nil {
		return nil, fmt.Errorf("final publication artifacts: %w", err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], terminalCommit.PayloadDigest)
	publishedAt := progression.RecordedAt
	publication := &projection.FinalPublication{
		IDs: projection.PublicationIDs{
			RevisionID: ids.ProjectionRevisionID,
			CutoffID:   ids.CutoffID,
		},
		Scope: projection.FinalScope{
			TournamentID:  stage.TournamentID,
			RosterID:      stage.RosterID,
			SeriesID:      stage.FinalSeriesID,
			GameAttemptID: currentAttempt.AttemptID,
		},
		Expected: projection.FinalHeadExpectation{
			TournamentRevision:     expectedTournamentRevision,
			ProjectionRevision:     expectedProjectionRevision,
			GameAttemptRevision:    currentAttempt.AttemptRevision,
			GameResultRevisionID:   *progression.Progression.Game.ResultRevisionID,
			SeriesRevision:         aggregate.SeriesRevision,
			ScoreHeadRevision:      scoreHead.HeadRevision,
			ScoreRevisionID:        progression.Progression.ScoreRevision.ID,
			SeriesResultRevisionID: *progression.Progression.TerminalResultRevisionID,
			WinnerID:               aggregate.WinnerID.UUID,
		},
		Artifacts:              artifacts,
		ResultProjectionDigest: digest,
		Reason:                 "final series completed",
		SupersessionReason:     "final champion publication",
		CutoffAt:               publishedAt,
		CreatedAt:              publishedAt,
		PublishedAt:            publishedAt,
	}
	if err := publication.Validate(); err != nil {
		return nil, fmt.Errorf("final publication validation (%w): %w", err, domain.ErrConflict)
	}
	return publication, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func finalPublicationArtifacts(
	ids playoff.FinalPublicationIDs,
	record ProjectionRecord,
	stage sqlc.LockPostseasonFinalStageRow,
	aggregate sqlc.LockFinalProjectionAggregateRow,
	semifinalBracket playoff.SemifinalAdvancementAuthority,
	advancement []playoff.SemifinalAdvancementResult,
	memberships []sqlc.LockFinalPublicationArtifactMembershipRow,
) ([]projection.PublicationArtifact, error) {
	winnerID := aggregate.WinnerID.UUID
	if len(record.Artifacts) != 3 || len(memberships) != 3 || winnerID == uuid.Nil ||
		record.Revision.TournamentID != stage.TournamentID || record.Revision.RosterID != stage.RosterID ||
		record.Revision.ID == uuid.Nil || record.Revision.RevisionNumber < 1 {
		return nil, domain.ErrConflict
	}
	byArtifact := make(map[uuid.UUID]sqlc.LockFinalPublicationArtifactMembershipRow, len(memberships))
	for _, membership := range memberships {
		if _, duplicate := byArtifact[membership.ArtifactID]; duplicate || membership.ArtifactID == uuid.Nil ||
			membership.RevisionID != record.Revision.ID || membership.TournamentID != stage.TournamentID ||
			membership.RosterID != stage.RosterID || membership.ProducedByRevisionID == uuid.Nil ||
			membership.ProducerRevision < 1 ||
			((membership.ChangeKind != "produced" || membership.ProducedByRevisionID != record.Revision.ID || membership.ProducerRevision != record.Revision.RevisionNumber) &&
				(membership.ChangeKind != "reused" || membership.ProducedByRevisionID == record.Revision.ID || membership.ProducerRevision >= record.Revision.RevisionNumber)) {
			return nil, domain.ErrConflict
		}
		byArtifact[membership.ArtifactID] = membership
	}
	source := make(map[domain.ArtifactKind]ProjectionArtifactRecord, len(record.Artifacts))
	for _, item := range record.Artifacts {
		kind := domain.ArtifactKind(item.Artifact.ArtifactKind)
		membership, linked := byArtifact[item.Artifact.ID]
		if kind != domain.ArtifactKindStandings && kind != domain.ArtifactKindBracket &&
			kind != domain.ArtifactKindTopFour || !linked || item.Artifact.TournamentID != stage.TournamentID ||
			item.Artifact.RosterID != stage.RosterID || membership.ArtifactKind != item.Artifact.ArtifactKind ||
			membership.ProducedByRevisionID != item.Artifact.ProducedByRevisionID ||
			len(item.Artifact.PayloadDigest) != sha256.Size || !bytes.Equal(membership.PayloadDigest, item.Artifact.PayloadDigest) {
			return nil, domain.ErrConflict
		}
		if _, exists := source[kind]; exists {
			return nil, domain.ErrConflict
		}
		source[kind] = item
	}
	standings, okStandings := source[domain.ArtifactKindStandings]
	bracket, okBracket := source[domain.ArtifactKindBracket]
	topFour, okTopFour := source[domain.ArtifactKindTopFour]
	if !okStandings || !okBracket || !okTopFour {
		return nil, domain.ErrConflict
	}
	seriesResultID := stage.CurrentResultRevisionID.UUID
	standingsArtifact, err := cloneFinalPublicationArtifact(
		standings,
		ids.StandingsArtifactID,
		"standings-final",
		[]projection.PublicationDependency{{
			ID: ids.StandingsDependencyID, Kind: projection.DependencyOfficialResult,
			OfficialResultRevisionID: &seriesResultID, OfficialResultSeriesID: &stage.FinalSeriesID,
		}},
	)
	if err != nil {
		return nil, err
	}
	bracketArtifact, err := finalBracketPublicationArtifact(
		bracket,
		ids.BracketArtifactID,
		stage,
		aggregate,
		semifinalBracket,
		advancement,
		[]projection.PublicationDependency{{
			ID: ids.BracketDependencyID, Kind: projection.DependencyArtifact,
			DependsOnArtifactID: &ids.StandingsArtifactID,
		}},
	)
	if err != nil {
		return nil, err
	}
	topFourArtifact, err := cloneFinalPublicationArtifact(
		topFour,
		ids.TopFourArtifactID,
		"top-four-final",
		[]projection.PublicationDependency{{
			ID: ids.TopFourDependencyID, Kind: projection.DependencyArtifact,
			DependsOnArtifactID: &ids.BracketArtifactID,
		}},
	)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]uuid.UUID{"participant_id": winnerID})
	if err != nil {
		return nil, err
	}
	champion := projection.PublicationArtifact{
		ID:            ids.ChampionArtifactID,
		Kind:          domain.ArtifactKindChampion,
		Key:           "champion-final",
		Payload:       payload,
		PayloadDigest: sha256.Sum256(payload),
		Members: []projection.PublicationMember{{
			ParticipantID: winnerID,
			Position:      1,
		}},
		Dependencies: []projection.PublicationDependency{
			{
				ID: ids.ChampionBracketDependencyID, Kind: projection.DependencyArtifact,
				DependsOnArtifactID: &ids.BracketArtifactID,
			},
			{
				ID: ids.ChampionResultDependencyID, Kind: projection.DependencyOfficialResult,
				OfficialResultRevisionID: &seriesResultID, OfficialResultSeriesID: &stage.FinalSeriesID,
			},
		},
	}
	return []projection.PublicationArtifact{
		standingsArtifact,
		bracketArtifact,
		topFourArtifact,
		champion,
	}, nil
}

type finalBracketPayloadDocument struct {
	Rounds []finalBracketPayloadMatch `json:"rounds"`
}

type finalBracketPayloadMatch struct {
	Stage               string             `json:"stage"`
	Position            int                `json:"position"`
	SeriesID            uuid.UUID          `json:"series_id"`
	FirstParticipantID  uuid.UUID          `json:"first_participant_id"`
	SecondParticipantID uuid.UUID          `json:"second_participant_id"`
	State               domain.SeriesState `json:"state"`
	FirstWins           int                `json:"first_wins"`
	SecondWins          int                `json:"second_wins"`
}

//nolint:gocyclo // Final publication validates the complete semifinal-to-final evidence chain as one artifact boundary.
func finalBracketPublicationArtifact(
	source ProjectionArtifactRecord,
	id uuid.UUID,
	stage sqlc.LockPostseasonFinalStageRow,
	aggregate sqlc.LockFinalProjectionAggregateRow,
	semifinalBracket playoff.SemifinalAdvancementAuthority,
	advancement []playoff.SemifinalAdvancementResult,
	dependencies []projection.PublicationDependency,
) (projection.PublicationArtifact, error) {
	artifact, err := cloneFinalPublicationArtifact(source, id, "bracket-final", dependencies)
	if err != nil {
		return projection.PublicationArtifact{}, err
	}
	if stage.TournamentID == uuid.Nil || stage.FinalSeriesID == uuid.Nil ||
		semifinalBracket.TournamentID != stage.TournamentID || len(semifinalBracket.Semifinals) != 2 ||
		len(advancement) != 2 || aggregate.SeriesState != string(domain.SeriesStateCompleted) ||
		aggregate.SeriesFormat != string(domain.SeriesFormatBO3) || !aggregate.WinnerID.Valid ||
		aggregate.FirstParticipantID != stage.FirstParticipantID ||
		aggregate.SecondParticipantID != stage.SecondParticipantID ||
		aggregate.WinnerID.UUID != aggregate.FirstParticipantID &&
			aggregate.WinnerID.UUID != aggregate.SecondParticipantID {
		return projection.PublicationArtifact{}, domain.ErrConflict
	}

	rounds := make([]finalBracketPayloadMatch, 0, 3)
	for index, match := range semifinalBracket.Semifinals {
		result := advancement[index]
		if match.Position != index+1 || match.Series.ID == uuid.Nil ||
			match.Series.TournamentID != stage.TournamentID ||
			match.Series.FirstParticipantID == uuid.Nil || match.Series.SecondParticipantID == uuid.Nil ||
			match.Series.FirstParticipantID == match.Series.SecondParticipantID ||
			result.Position != match.Position || result.SeriesID != match.Series.ID ||
			result.WinnerID == uuid.Nil || result.LoserID == uuid.Nil || result.WinnerID == result.LoserID {
			return projection.PublicationArtifact{}, domain.ErrConflict
		}
		firstWins, secondWins := 0, 0
		switch {
		case result.WinnerID == match.Series.FirstParticipantID && result.LoserID == match.Series.SecondParticipantID:
			firstWins = 1
		case result.WinnerID == match.Series.SecondParticipantID && result.LoserID == match.Series.FirstParticipantID:
			secondWins = 1
		default:
			return projection.PublicationArtifact{}, domain.ErrConflict
		}
		rounds = append(rounds, finalBracketPayloadMatch{
			Stage: "semifinal", Position: match.Position, SeriesID: match.Series.ID,
			FirstParticipantID: match.Series.FirstParticipantID, SecondParticipantID: match.Series.SecondParticipantID,
			State: domain.SeriesStateCompleted, FirstWins: firstWins, SecondWins: secondWins,
		})
	}
	rounds = append(rounds, finalBracketPayloadMatch{
		Stage: "final", Position: 1, SeriesID: stage.FinalSeriesID,
		FirstParticipantID: aggregate.FirstParticipantID, SecondParticipantID: aggregate.SecondParticipantID,
		State: domain.SeriesStateCompleted, FirstWins: int(aggregate.FirstParticipantWins),
		SecondWins: int(aggregate.SecondParticipantWins),
	})
	payload, err := json.Marshal(finalBracketPayloadDocument{Rounds: rounds})
	if err != nil {
		return projection.PublicationArtifact{}, domain.ErrConflict
	}
	artifact.Payload = payload
	artifact.PayloadDigest = sha256.Sum256(payload)
	return artifact, nil
}

func cloneFinalPublicationArtifact(
	source ProjectionArtifactRecord,
	id uuid.UUID,
	key string,
	dependencies []projection.PublicationDependency,
) (projection.PublicationArtifact, error) {
	if id == uuid.Nil || key == "" || len(source.Artifact.PayloadDigest) != sha256.Size ||
		!json.Valid(source.Artifact.Payload) || len(source.Members) == 0 || len(dependencies) == 0 {
		return projection.PublicationArtifact{}, domain.ErrConflict
	}
	digest := sha256.Sum256(source.Artifact.Payload)
	if !reflect.DeepEqual(digest[:], source.Artifact.PayloadDigest) {
		return projection.PublicationArtifact{}, domain.ErrConflict
	}
	members := make([]projection.PublicationMember, 0, len(source.Members))
	for _, member := range source.Members {
		var score *int64
		if source.Artifact.ArtifactKind == string(domain.ArtifactKindStandings) {
			value, err := terminalScoreMilli(member.Score)
			if err != nil || value == nil {
				return projection.PublicationArtifact{}, domain.ErrConflict
			}
			score = value
		} else if member.Score.Valid {
			return projection.PublicationArtifact{}, domain.ErrConflict
		}
		members = append(members, projection.PublicationMember{
			ParticipantID: member.ParticipantID,
			Position:      member.Position,
			ScoreMilli:    score,
		})
	}
	return projection.PublicationArtifact{
		ID:            id,
		Kind:          domain.ArtifactKind(source.Artifact.ArtifactKind),
		Key:           key,
		Payload:       append([]byte(nil), source.Artifact.Payload...),
		PayloadDigest: digest,
		Members:       members,
		Dependencies:  dependencies,
	}, nil
}

func terminalScoreMilli(value pgtype.Numeric) (*int64, error) {
	result, err := progressionScoreMilli(value)
	if err != nil || result == nil || *result < 0 {
		return nil, domain.ErrConflict
	}
	return result, nil
}
