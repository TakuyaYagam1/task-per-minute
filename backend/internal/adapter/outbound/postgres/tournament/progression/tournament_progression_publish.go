package progression

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

type progressionProof struct {
	Action           tournamentprogression.Action `json:"action"`
	CommandID        uuid.UUID                    `json:"command_id"`
	TournamentID     uuid.UUID                    `json:"tournament_id"`
	RosterID         uuid.UUID                    `json:"roster_id"`
	SourceState      domain.TournamentState       `json:"source_state"`
	SourceRevisionID uuid.UUID                    `json:"source_revision_id"`
	SourceRevision   int64                        `json:"source_revision"`
	SourceDigest     string                       `json:"source_digest"`
	PlannedAt        time.Time                    `json:"planned_at"`
}

func progressionPlanTime(plan tournamentprogression.Plan) (time.Time, error) {
	var proof progressionProof
	if !progressionPlanProof(plan, &proof) || !progressionProofIdentity(plan, proof) || !progressionFinalSwissMatches(plan, proof) || !progressionStandingsMatch(plan) {
		return time.Time{}, domain.ErrValidation
	}
	if plan.Record.Command.Action == tournamentprogression.ActionStartGolden {
		if !progressionGoldenPlanMatches(plan) {
			return time.Time{}, domain.ErrValidation
		}
	} else if !progressionPlayoffPlanMatches(plan, proof) || !progressionPlayoffLineageMatches(plan) {
		return time.Time{}, domain.ErrValidation
	}
	return proof.PlannedAt, nil
}

func progressionPlanProof(plan tournamentprogression.Plan, proof *progressionProof) bool {
	r := plan.Record
	if !validTournamentProgressionCommand(r.Command) || r.Source.ArtifactID == uuid.Nil || r.Source.RevisionID == uuid.Nil ||
		r.Source.Kind != domain.ArtifactKindStandings || r.Source.Revision != r.Command.ExpectedProjectionRevision ||
		sha256.Sum256(r.Proof) != r.ProofDigest || json.Unmarshal(r.Proof, proof) != nil ||
		!json.Valid(r.Source.Payload) || sha256.Sum256(r.Source.Payload) != r.Source.Digest {
		return false
	}
	return true
}

func progressionProofIdentity(plan tournamentprogression.Plan, proof progressionProof) bool {
	r := plan.Record
	if proof.Action != r.Command.Action || proof.CommandID != r.Command.CommandID || proof.TournamentID != r.Command.TournamentID ||
		proof.RosterID != r.Command.RosterID || proof.SourceState != r.SourceState || proof.SourceRevisionID != r.Source.RevisionID ||
		proof.SourceRevision != r.Source.Revision || proof.SourceDigest != hex.EncodeToString(r.Source.Digest[:]) {
		return false
	}
	return true
}

func progressionFinalSwissMatches(plan tournamentprogression.Plan, proof progressionProof) bool {
	r := plan.Record
	if !domain.IsValidServerTime(proof.PlannedAt) || plan.FinalSwiss.Validate() != nil ||
		plan.FinalSwiss.Projection().Revision().TournamentID() != r.Command.TournamentID ||
		plan.FinalSwiss.Projection().Revision().ID().UUID() != r.Source.RevisionID ||
		plan.FinalSwiss.PhysicalProjectionRevision() != int(r.Source.Revision) {
		return false
	}
	return true
}

func progressionStandingsMatch(plan tournamentprogression.Plan) bool {
	r := plan.Record
	standings := plan.FinalSwiss.Standings()
	if len(r.Source.Members) != len(standings) {
		return false
	}
	for i, standing := range standings {
		member := r.Source.Members[i]
		if member.ParticipantID != standing.ParticipantID || member.Position != standing.Position || member.ScoreMilli == nil || *member.ScoreMilli != int64(standing.Points)*1000 {
			return false
		}
	}

	return true
}

func progressionGoldenPlanMatches(plan tournamentprogression.Plan) bool {
	r := plan.Record

	if r.SourceState != domain.TournamentStateSwiss || plan.Top4 != nil || plan.Bracket != nil ||
		plan.FinalSwiss.AdvanceDirectly() || len(r.TieGroups) == 0 || len(plan.GoldenGroups) != len(r.TieGroups) {
		return false
	}
	canonicalGroups := plan.FinalSwiss.GoldenGroups()
	if len(canonicalGroups) != len(plan.GoldenGroups) {
		return false
	}
	for i, group := range canonicalGroups {
		if plan.GoldenGroups[i].Revision.Validate() != nil || group.Revision.PayloadDigest() != plan.GoldenGroups[i].Revision.PayloadDigest() ||
			!progressionBytesEqual(group.Revision.Payload(), plan.GoldenGroups[i].Revision.Payload()) {
			return false
		}
	}
	return true
}

func progressionPlayoffPlanMatches(plan tournamentprogression.Plan, proof progressionProof) bool {
	r := plan.Record

	ids, err := tournamentprogression.PlayoffPublicationIdentity(r.Command.CommandID)
	if err != nil || ids != plan.PublicationIDs || plan.Top4 == nil || plan.Bracket == nil ||
		plan.Top4.Validate() != nil || plan.Bracket.Validate() != nil || !plan.Bracket.Locked() ||
		len(r.TieGroups) != 0 || len(plan.GoldenGroups) != 0 {
		return false
	}
	return progressionPlayoffScopeMatches(plan, proof)
}

func progressionPlayoffScopeMatches(plan tournamentprogression.Plan, proof progressionProof) bool {
	r := plan.Record
	if (r.SourceState != domain.TournamentStateSwiss && r.SourceState != domain.TournamentStateGolden) ||
		plan.Top4.Projection().Revision().TournamentID() != r.Command.TournamentID ||
		plan.Bracket.Projection().Revision().TournamentID() != r.Command.TournamentID ||
		!plan.Top4.Projection().Revision().CreatedAt().Equal(proof.PlannedAt) || !plan.Bracket.LockedAt().Equal(proof.PlannedAt) {
		return false
	}
	return true
}

func progressionPlayoffLineageMatches(plan tournamentprogression.Plan) bool {
	var top4Source struct {
		RevisionID uuid.UUID `json:"final_standings_revision_id"`
		Digest     string    `json:"final_standings_digest"`
	}
	var bracketSource struct {
		RevisionID uuid.UUID `json:"top4_revision_id"`
		Digest     string    `json:"top4_digest"`
	}
	swissDigest := plan.FinalSwiss.Projection().Revision().PayloadDigest()
	top4Digest := plan.Top4.Projection().Revision().PayloadDigest()
	if json.Unmarshal(plan.Top4.Projection().Payload(), &top4Source) != nil || top4Source.RevisionID != plan.FinalSwiss.Projection().Revision().ID().UUID() || top4Source.Digest != hex.EncodeToString(swissDigest[:]) ||
		json.Unmarshal(plan.Bracket.Projection().Payload(), &bracketSource) != nil || bracketSource.RevisionID != plan.Top4.Projection().Revision().ID().UUID() || bracketSource.Digest != hex.EncodeToString(top4Digest[:]) {
		return false
	}
	return true
}

func progressionWrittenID(operation string, want, got uuid.UUID, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return mapRepositoryWriteError("TournamentProgressionPostgres - "+operation, err)
	}
	if want == uuid.Nil || got != want {
		return domain.ErrConflict
	}
	return nil
}

func (r *TournamentProgressionPostgres) PublishPlayoffStage(ctx context.Context, plan tournamentprogression.Plan) (tournamentprogression.PlayoffPublication, error) {
	if r == nil || r.tx == nil || ctx == nil || plan.Record.Command.Action != tournamentprogression.ActionStartPlayoffs {
		return tournamentprogression.PlayoffPublication{}, domain.ErrValidation
	}
	now, err := progressionPlanTime(plan)
	if err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	var publication tournamentprogression.PlayoffPublication
	err = r.tx.Do(ctx, func(txCtx context.Context) error {
		publication, err = r.publishPlayoffStage(txCtx, plan, now)
		return err
	})
	if err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	return publication, nil
}

func loadProgressionSourceArtifact(ctx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan) (sqlc.ProjectionArtifact, error) {
	command := plan.Record.Command
	artifact, err := q.GetProjectionArtifactScoped(ctx, sqlc.GetProjectionArtifactScopedParams{ID: plan.Record.Source.ArtifactID, TournamentID: command.TournamentID, RosterID: command.RosterID})
	if err != nil {
		return sqlc.ProjectionArtifact{}, projectionCASWriteError("read source artifact", err)
	}
	if !progressionSourceArtifactMatches(plan, artifact) {
		return sqlc.ProjectionArtifact{}, domain.ErrConflict
	}
	links, err := q.ListProjectionRevisionArtifacts(ctx, sqlc.ListProjectionRevisionArtifactsParams{RevisionID: plan.Record.Source.RevisionID, TournamentID: command.TournamentID, RosterID: command.RosterID})
	if err != nil {
		return sqlc.ProjectionArtifact{}, tournamentProgressionReadError("read source artifact links", err)
	}
	found := false
	for _, link := range links {
		if link.ArtifactKind != string(domain.ArtifactKindStandings) {
			continue
		}
		if found || link.ArtifactID != artifact.ID || link.RevisionID != plan.Record.Source.RevisionID || link.TournamentID != command.TournamentID || link.RosterID != command.RosterID {
			return sqlc.ProjectionArtifact{}, domain.ErrConflict
		}
		found = true
	}
	if !found {
		return sqlc.ProjectionArtifact{}, domain.ErrConflict
	}
	return artifact, nil
}

func progressionPublicationInput(plan tournamentprogression.Plan, source sqlc.ProjectionArtifact, now time.Time) (ProjectionPublishInput, error) {
	command, ids := plan.Record.Command, plan.PublicationIDs
	standings, err := progressionStandingsArtifact(plan, source)
	if err != nil {
		return ProjectionPublishInput{}, err
	}
	top4 := ProjectionArtifactInput{ID: ids.Top4ArtifactID, Kind: domain.ArtifactKindTopFour, Key: "top_four", Payload: plan.Top4.Projection().Payload(), PayloadDigest: plan.Top4.Projection().Revision().PayloadDigest(),
		Dependencies: []ProjectionDependencyInput{{ID: ids.Top4DependencyID, Kind: projectionDependencyArtifact, DependsOnArtifactID: &ids.StandingsArtifactID}}}
	goldenDependencies, err := progressionGoldenDependencies(ids, command.CommandID, plan.Top4.GoldenPositionCommitIDs())
	if err != nil {
		return ProjectionPublishInput{}, err
	}
	top4.Dependencies = append(top4.Dependencies, goldenDependencies...)
	if (plan.Record.SourceState == domain.TournamentStateGolden) != (len(goldenDependencies) > 0) {
		return ProjectionPublishInput{}, domain.ErrConflict
	}
	bracket := ProjectionArtifactInput{ID: ids.BracketArtifactID, Kind: domain.ArtifactKindBracket, Key: "bracket", Payload: plan.Bracket.Projection().Payload(), PayloadDigest: plan.Bracket.Projection().Revision().PayloadDigest(),
		Dependencies: []ProjectionDependencyInput{{ID: ids.BracketDependencyID, Kind: projectionDependencyArtifact, DependsOnArtifactID: &ids.Top4ArtifactID}}}
	for _, member := range plan.Top4.Participants() {
		position, err := progressionInt32(member.Seed)
		if err != nil {
			return ProjectionPublishInput{}, err
		}
		value := ProjectionMemberInput{ParticipantID: member.ParticipantID, Position: position}
		top4.Members = append(top4.Members, value)
		bracket.Members = append(bracket.Members, value)
	}
	input := ProjectionPublishInput{IDs: ProjectionIDs{RevisionID: ids.ProjectionRevisionID, CutoffID: ids.CutoffID}, Scope: ProjectionScope{TournamentID: command.TournamentID, RosterID: command.RosterID},
		Source:    ProjectionSource{Kind: projectionSourceStageProgression, StageProgressionCommandID: &command.CommandID, Reason: string(command.Action)},
		Artifacts: []ProjectionArtifactInput{standings, top4, bracket}, SupersessionReason: string(command.Action), CutoffAt: now, CreatedAt: now, PublishedAt: now}
	if !validProjectionPublishInput(input) {
		return ProjectionPublishInput{}, domain.ErrConflict
	}
	return input, nil
}

func progressionGoldenDependencies(ids tournamentprogression.PlayoffPublicationIDs, commandID uuid.UUID, commits []uuid.UUID) ([]ProjectionDependencyInput, error) {
	dependencies := make([]ProjectionDependencyInput, 0, len(commits))
	seen := make(map[uuid.UUID]struct{}, len(commits))
	for _, commitID := range commits {
		if _, duplicate := seen[commitID]; duplicate {
			return nil, domain.ErrConflict
		}
		seen[commitID] = struct{}{}
		id, err := ids.GoldenDependencyID(commandID, commitID)
		if err != nil {
			return nil, err
		}
		dependencies = append(dependencies, ProjectionDependencyInput{ID: id, Kind: projectionDependencyGoldenPosition, GoldenPositionCommitID: &commitID})
	}
	return dependencies, nil
}

func progressionPublishedRecord(plan tournamentprogression.Plan, input ProjectionPublishInput, record *ProjectionRecord) error {
	if record == nil || !progressionPublishedRevisionMatches(plan, input, record) || !progressionPublishedCutoffMatches(plan, input, record) {
		return domain.ErrConflict
	}
	for _, want := range input.Artifacts {
		found := false
		for _, got := range record.Artifacts {
			if got.Artifact.ID != want.ID {
				continue
			}
			if found || !progressionPublishedArtifactMatches(input, want, got) {
				return domain.ErrConflict
			}
			if err := progressionPublishedMembersMatch(want, got); err != nil {
				return err
			}
			if err := progressionPublishedDependenciesMatch(input, want, got); err != nil {
				return err
			}
			found = true
		}
		if !found {
			return domain.ErrConflict
		}
	}
	return nil
}

func (r *TournamentProgressionPostgres) publishPlayoffStage(txCtx context.Context, plan tournamentprogression.Plan, now time.Time) (tournamentprogression.PlayoffPublication, error) {
	q := r.tx.Querier(txCtx)
	command := plan.Record.Command
	if err := r.validateProgressionPublicationAuthority(txCtx, q, plan, now); err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	if err := checkProgressionReplay(txCtx, q, command, "check publication replay"); err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	if err := validateProgressionPublishedSource(txCtx, q, plan); err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	artifact, err := loadProgressionSourceArtifact(txCtx, q, plan)
	if err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	input, err := progressionPublicationInput(plan, artifact, now)
	if err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	matches := plan.Bracket.Semifinals()
	if err := createProgressionSemifinals(txCtx, q, plan, now); err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	record, err := NewProjectionPostgres(r.tx).Publish(txCtx, input)
	if err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	if err := progressionPublishedRecord(plan, input, record); err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	publication := tournamentprogression.PlayoffPublication{
		PublishedProjectionID: record.Revision.ID, PublishedRevision: record.Revision.RevisionNumber,
		Top4ArtifactID: plan.PublicationIDs.Top4ArtifactID, BracketArtifactID: plan.PublicationIDs.BracketArtifactID,
		SemifinalSeriesIDs: [2]uuid.UUID{matches[0].Series.ID, matches[1].Series.ID},
	}
	if err := r.materializePlayoffSemifinals(txCtx, plan, publication, now); err != nil {
		return tournamentprogression.PlayoffPublication{}, err
	}
	return publication, nil
}

func (r *TournamentProgressionPostgres) validateProgressionPublicationAuthority(txCtx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan, now time.Time) error {
	command := plan.Record.Command
	tournament, err := q.GetTournamentSummary(txCtx, command.TournamentID)
	if err != nil {
		return projectionCASWriteError("read publication tournament", err)
	}
	view, err := tournamentProgressionTournamentView(tournament)
	if err != nil {
		return err
	}
	if view.ID != command.TournamentID || view.RosterID != command.RosterID || view.State != plan.Record.SourceState || !now.After(view.UpdatedAt) {
		return domain.ErrConflict
	}
	if view.State == domain.TournamentStateGolden {
		settlements, err := r.loadGoldenSettlements(txCtx, tournamentprogression.Authority{Tournament: view, ProjectionRevisionID: plan.Record.Source.RevisionID, ProjectionRevision: plan.Record.Source.Revision})
		if err != nil {
			return err
		}
		return progressionPublicationCommitsMatch(plan, settlements)
	}
	return nil
}

func progressionPublicationCommitsMatch(plan tournamentprogression.Plan, settlements []playoff.Top4GoldenSettlement) error {
	commits := make(map[uuid.UUID]struct{})
	for _, settlement := range settlements {
		for _, position := range settlement.Positions.Positions() {
			commits[position.CommitID] = struct{}{}
		}
	}
	if len(commits) != len(plan.Top4.GoldenPositionCommitIDs()) {
		return domain.ErrConflict
	}
	for _, id := range plan.Top4.GoldenPositionCommitIDs() {
		if _, found := commits[id]; !found {
			return domain.ErrConflict
		}
		delete(commits, id)
	}
	if len(commits) != 0 {
		return domain.ErrConflict
	}
	return nil
}

func validateProgressionPublishedSource(txCtx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan) error {
	command := plan.Record.Command
	source, err := q.GetProjectionRevisionScoped(txCtx, sqlc.GetProjectionRevisionScopedParams{ID: plan.Record.Source.RevisionID, TournamentID: command.TournamentID, RosterID: command.RosterID})
	if err != nil {
		return projectionCASWriteError("read stage source", err)
	}
	if source.ID != plan.Record.Source.RevisionID || source.TournamentID != command.TournamentID || source.RosterID != command.RosterID ||
		source.State != "published" || source.SupersededByRevisionID.Valid || source.RevisionNumber != plan.Record.Source.Revision {
		return domain.ErrConflict
	}
	return nil
}

func createProgressionSemifinals(txCtx context.Context, q *sqlc.Queries, plan tournamentprogression.Plan, now time.Time) error {
	command, matches := plan.Record.Command, plan.Bracket.Semifinals()
	scores := [2]domain.SeriesScoreRevisionID{plan.PublicationIDs.FirstSemifinalScoreRevisionID, plan.PublicationIDs.SecondSemifinalScoreRevisionID}
	for index, match := range matches {
		id, writeErr := q.CreateTournamentProgressionLockedSemifinalSeries(txCtx, sqlc.CreateTournamentProgressionLockedSemifinalSeriesParams{
			SeriesID: match.Series.ID, TournamentID: command.TournamentID, RosterID: command.RosterID,
			FirstParticipantID: match.Series.FirstParticipantID, SecondParticipantID: match.Series.SecondParticipantID,
			InitialScoreRevisionID: nullableUUIDValue(scores[index].UUID()), CreatedAt: tstz(now), CommandID: command.CommandID,
			SourceProjectionRevisionID: plan.Record.Source.RevisionID, SourceProjectionRevision: plan.Record.Source.Revision,
		})
		if err := progressionWrittenID("create semifinal", match.Series.ID, id, writeErr); err != nil {
			return err
		}
	}
	return nil
}

func progressionSourceArtifactMatches(plan tournamentprogression.Plan, source sqlc.ProjectionArtifact) bool {
	command := plan.Record.Command
	if source.ID != plan.Record.Source.ArtifactID || source.TournamentID != command.TournamentID || source.RosterID != command.RosterID ||
		source.ArtifactKind != string(domain.ArtifactKindStandings) || sha256.Sum256(plan.Record.Source.Payload) != plan.Record.Source.Digest ||
		!progressionJSONEqual(source.Payload, plan.Record.Source.Payload) ||
		!progressionBytesEqual(source.PayloadDigest, plan.Record.Source.Digest[:]) {
		return false
	}
	return true
}

func progressionPublishedRevisionMatches(plan tournamentprogression.Plan, input ProjectionPublishInput, record *ProjectionRecord) bool {
	if record.Revision.ID != input.IDs.RevisionID || record.Revision.TournamentID != input.Scope.TournamentID || record.Revision.RosterID != input.Scope.RosterID ||
		record.Revision.State != "published" || record.Revision.SupersededByRevisionID.Valid || record.Revision.RevisionNumber != plan.Record.Source.Revision+1 ||
		!record.Revision.PreviousRevisionID.Valid || record.Revision.PreviousRevisionID.UUID != plan.Record.Source.RevisionID {
		return false
	}
	return true
}

func progressionPublishedCutoffMatches(plan tournamentprogression.Plan, input ProjectionPublishInput, record *ProjectionRecord) bool {
	if record.Revision.CutoffID != input.IDs.CutoffID || record.Cutoff.ID != input.IDs.CutoffID ||
		!record.Cutoff.StageProgressionCommandID.Valid || record.Cutoff.StageProgressionCommandID.UUID != plan.Record.Command.CommandID || len(record.Artifacts) != len(input.Artifacts) {
		return false
	}
	return true
}

func progressionPublishedArtifactMatches(input ProjectionPublishInput, want ProjectionArtifactInput, got ProjectionArtifactRecord) bool {
	if got.Artifact.TournamentID != input.Scope.TournamentID || got.Artifact.RosterID != input.Scope.RosterID || got.Artifact.ProducedByRevisionID != input.IDs.RevisionID ||
		got.Artifact.ArtifactKind != string(want.Kind) || got.Artifact.ArtifactKey != want.Key || !progressionJSONEqual(got.Artifact.Payload, want.Payload) ||
		!progressionBytesEqual(got.Artifact.PayloadDigest, want.PayloadDigest[:]) || len(got.Members) != len(want.Members) || len(got.Dependencies) != len(want.Dependencies) {
		return false
	}
	return true
}

func progressionPublishedMembersMatch(want ProjectionArtifactInput, got ProjectionArtifactRecord) error {
	for i, member := range got.Members {
		expected := want.Members[i]
		if member.ArtifactID != want.ID || member.ParticipantID != expected.ParticipantID || member.Position != expected.Position {
			return domain.ErrConflict
		}
		if expected.ScoreMilli == nil {
			if member.Score.Valid {
				return domain.ErrConflict
			}
		} else {
			score, err := progressionScoreMilli(member.Score)
			if err != nil || *score != *expected.ScoreMilli {
				return domain.ErrConflict
			}
		}
	}
	return nil
}

func progressionPublishedDependenciesMatch(input ProjectionPublishInput, want ProjectionArtifactInput, got ProjectionArtifactRecord) error {
	seenDependencies := make(map[uuid.UUID]struct{})
	for _, dependency := range got.Dependencies {
		if _, duplicate := seenDependencies[dependency.ID]; duplicate {
			return domain.ErrConflict
		}
		seenDependencies[dependency.ID] = struct{}{}
		matched := false
		for _, wantDependency := range want.Dependencies {
			if progressionPublishedDependencyMatches(input, want.ID, wantDependency, dependency) {
				matched = true
			}
		}
		if !matched {
			return domain.ErrConflict
		}
	}
	return nil
}

func progressionPublishedDependencyMatches(input ProjectionPublishInput, artifactID uuid.UUID, wantDependency ProjectionDependencyInput, dependency sqlc.ProjectionDependency) bool {
	return dependency.ID == wantDependency.ID && dependency.ArtifactID == artifactID && dependency.TournamentID == input.Scope.TournamentID && dependency.RosterID == input.Scope.RosterID && dependency.DependencyKind == wantDependency.Kind &&
		dependency.DependsOnArtifactID == nullableUUID(wantDependency.DependsOnArtifactID) && dependency.GoldenPositionCommitID == nullableUUID(wantDependency.GoldenPositionCommitID) &&
		!dependency.OfficialResultRevisionID.Valid && !dependency.OfficialResultSeriesID.Valid
}

func progressionStandingsArtifact(plan tournamentprogression.Plan, source sqlc.ProjectionArtifact) (ProjectionArtifactInput, error) {
	ids := plan.PublicationIDs
	if !progressionSourceArtifactMatches(plan, source) {
		return ProjectionArtifactInput{}, domain.ErrConflict
	}
	standings := ProjectionArtifactInput{ID: ids.StandingsArtifactID, Kind: domain.ArtifactKindStandings, Key: "standings", Payload: append([]byte(nil), plan.Record.Source.Payload...), PayloadDigest: plan.Record.Source.Digest,
		Dependencies: []ProjectionDependencyInput{{ID: ids.StandingsDependencyID, Kind: projectionDependencyArtifact, DependsOnArtifactID: &source.ID}}}
	for _, member := range plan.Record.Source.Members {
		position, err := progressionInt32(member.Position)
		if err != nil {
			return ProjectionArtifactInput{}, err
		}
		standings.Members = append(standings.Members, ProjectionMemberInput{ParticipantID: member.ParticipantID, Position: position, ScoreMilli: member.ScoreMilli})
	}
	return standings, nil
}
