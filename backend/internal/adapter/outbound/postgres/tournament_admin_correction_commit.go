package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func (r *TournamentAdminCorrectionPostgres) buildCorrectionCommit(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	scope ResultScope,
) (CorrectionInput, tournamentAdminCorrectionLogicalPlan, error) {
	current, err := querier.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return CorrectionInput{}, tournamentAdminCorrectionLogicalPlan{}, tournamentAdminCorrectionError("recheck current projection", err)
	}
	if current.ID != mutation.Authority.ProjectionRevisionID || current.RevisionNumber != mutation.Authority.ProjectionRevision {
		return CorrectionInput{}, tournamentAdminCorrectionLogicalPlan{}, domain.ErrConflict
	}
	rows, err := querier.ListTournamentAdminCorrectionGameResults(ctx, sqlc.ListTournamentAdminCorrectionGameResultsParams{
		SeriesID: scope.SeriesID, RosterID: scope.RosterID, TournamentID: scope.TournamentID,
	})
	if err != nil {
		return CorrectionInput{}, tournamentAdminCorrectionLogicalPlan{}, tournamentAdminCorrectionError("recheck result heads", err)
	}
	target, found := correctionTargetGameRow(rows, scope.AttemptID)
	condition := mutation.Plan.GameResultRevision().Condition()
	expectedGameID := condition.ExpectedCurrentRevisionID()
	if !found || expectedGameID == nil || target.ResultRevisionID != expectedGameID.UUID() ||
		target.ResultRevisionID != mutation.Authority.Core.GameResult.ID.UUID() {
		return CorrectionInput{}, tournamentAdminCorrectionLogicalPlan{}, domain.ErrConflict
	}

	artifacts, artifactIDs, reusedArtifactIDs, err := r.materializedCorrectionArtifacts(ctx, querier, mutation, scope)
	if err != nil {
		return CorrectionInput{}, tournamentAdminCorrectionLogicalPlan{}, err
	}
	logical, err := correctionLogicalBindings(mutation, scope, artifactIDs)
	if err != nil {
		return CorrectionInput{}, tournamentAdminCorrectionLogicalPlan{}, err
	}
	gameRevision := mutation.Plan.GameResultRevision().Revision()
	scoreRevision := mutation.Plan.ScoreRevision().Revision()
	seriesRevision := mutation.Plan.SeriesResultRevision().Revision()
	projectedSeries := mutation.Plan.Series()
	if gameRevision.ID().UUID() == uuid.Nil || scoreRevision.ID().UUID() == uuid.Nil || seriesRevision.ID().UUID() == uuid.Nil ||
		projectedSeries.CurrentScoreRevisionID == nil || projectedSeries.CurrentResultRevisionID == nil {
		return CorrectionInput{}, tournamentAdminCorrectionLogicalPlan{}, domain.ErrConflict
	}
	solve := mutation.Plan.SolveMetadata()
	submissionEventID := uuid.Nil
	if solve.SubmissionID != nil {
		submissionEventID = *solve.SubmissionID
	}
	gameOutcome := gameRevision.Outcome()
	seriesOutcome := seriesRevision.Outcome()
	payloadDigest := sha256.Sum256(mutation.Plan.RebuildBytes())
	settlementIDs := correctionSettlementIDs(mutation.Command.CommandID)
	settlementIDs.GameResultRevisionID = gameRevision.ID().UUID()
	settlementIDs.SeriesScoreRevisionID = scoreRevision.ID().UUID()
	settlementIDs.SeriesResultRevisionID = seriesRevision.ID().UUID()
	input := CorrectionInput{
		IDs: settlementIDs,
		ProjectionIDs: ProjectionIDs{
			RevisionID: correctionWorkflowUUID(mutation.Command.CommandID, "projection-revision"),
			CutoffID:   correctionWorkflowUUID(mutation.Command.CommandID, "projection-cutoff"),
		},
		Scope: scope, SourceRevisionID: expectedGameID.UUID(),
		ExpectedAttemptRevision: int64(condition.ExpectedAttemptRevision()), ExpectedAttemptState: mutation.Authority.Core.GameResult.Outcome.GameState,
		ExpectedScoreRevisionID: mutation.Authority.Core.Score.ID.UUID(), ExpectedScoreRevision: int64(mutation.Authority.Core.Score.Ordinal),
		ExpectedSeriesRevision: int64(mutation.Authority.Core.SeriesRevision), ExpectedSeriesState: mutation.Authority.Core.Series.State,
		ExpectedSeriesResultRevisionID: correctionOfficialUUIDPointer(mutation.Authority.Core.SeriesResult.ID),
		ExpectedProjectionRevisionID:   mutation.Authority.ProjectionRevisionID,
		SubmissionEventID:              submissionEventID,
		GameResultCommandID:            gameRevision.CommandID(),
		ScoreCommandID:                 scoreRevision.CommandID(),
		SeriesResultCommandID:          seriesRevision.CommandID(),
		GameState:                      gameOutcome.GameState, GameReason: gameOutcome.GameReason, GameWinnerID: gameOutcome.WinnerID,
		Score: projectedSeries.Score, NextSeriesState: projectedSeries.State, SeriesResultReason: string(seriesOutcome.SeriesReason),
		SeriesWinnerID: seriesOutcome.WinnerID, OperatorID: mutation.Command.Operator.ActorID, Reason: mutation.Command.Reason,
		ProjectionArtifacts: artifacts, ReusedProjectionArtifactIDs: reusedArtifactIDs, ReplaceProjectionSet: true,
		ProjectionPayloadDigest: payloadDigest, CorrectedAt: mutation.Evidence.RequestedAt,
	}
	if !validCorrectionInput(input) {
		return CorrectionInput{}, tournamentAdminCorrectionLogicalPlan{}, fmt.Errorf(
			"%w: correction input identity=%t metadata=%t states=%t game=%t series=%t attempt_rev=%d score_rev=%d series_rev=%d corrected_at=%s reason=%q payload=%x artifacts=%d",
			domain.ErrValidation,
			validCorrectionIdentity(input),
			validCorrectionMetadata(input),
			validCorrectionStates(input),
			validCorrectionGame(input),
			validCorrectionSeries(input),
			input.ExpectedAttemptRevision,
			input.ExpectedScoreRevision,
			input.ExpectedSeriesRevision,
			input.CorrectedAt.Format(time.RFC3339Nano),
			input.Reason,
			input.ProjectionPayloadDigest,
			len(input.ProjectionArtifacts),
		)
	}
	return input, logical, nil
}

func correctionSettlementIDs(commandID uuid.UUID) ResultSettlementIDs {
	return ResultSettlementIDs{
		CommitID:                  correctionWorkflowUUID(commandID, "result-commit"),
		ResultEventID:             correctionWorkflowUUID(commandID, "result-event"),
		ResultEventIdempotencyKey: correctionWorkflowUUID(commandID, "result-event-idempotency"),
		GameResultRevisionID:      correctionWorkflowUUID(commandID, "game-result-revision"),
		SeriesScoreRevisionID:     correctionWorkflowUUID(commandID, "series-score-revision"),
		SeriesResultRevisionID:    correctionWorkflowUUID(commandID, "series-result-revision"),
		AuditEventID:              correctionWorkflowUUID(commandID, "audit-event"),
		OutboxEventID:             correctionWorkflowUUID(commandID, "outbox-event"),
		OutboxIdempotencyKey:      correctionWorkflowUUID(commandID, "outbox-idempotency"),
		ProjectionEvidenceID:      correctionWorkflowUUID(commandID, "projection-evidence"),
		CommitIdempotencyKey:      commandID,
	}
}

func persistTournamentAdminCorrectionSwissLedger(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	scope ResultScope,
) error {
	ledger, err := correctionusecase.ApplyServerOwnedSwissSuccessor(
		mutation.Plan,
		mutation.Authority.Stage.Swiss.Ledger,
	)
	if err != nil {
		return err
	}
	wantedRevisionID := mutation.Plan.SeriesResultRevision().Revision().ID().UUID()
	writtenParticipants := make(map[uuid.UUID]struct{}, 2)
	for _, entry := range ledger {
		if entry.SourceKind != swissusecase.PointSourceSeries || entry.SourceSeriesID != scope.SeriesID ||
			entry.SeriesResultRevisionID != wantedRevisionID {
			continue
		}
		if entry.ParticipantID == uuid.Nil || entry.OpponentID == nil || *entry.OpponentID == uuid.Nil ||
			entry.ParticipantID == *entry.OpponentID || entry.RoundID == uuid.Nil || entry.RoundNumber < 1 ||
			entry.StableSeed < 1 || int(int32(entry.StableSeed)) != entry.StableSeed {
			return domain.ErrConflict
		}
		if _, duplicate := writtenParticipants[entry.ParticipantID]; duplicate {
			return domain.ErrConflict
		}
		roundNumber, conversionErr := correctionInt16(entry.RoundNumber)
		if conversionErr != nil {
			return conversionErr
		}
		if entry.Points < 0 || entry.Points > domain.TournamentMaxParticipants {
			return domain.ErrValidation
		}
		points := int16(entry.Points) //nolint:gosec // bounded by TournamentMaxParticipants.
		var accepted *int64
		if entry.AcceptedSolveTime != nil {
			value := int64(*entry.AcceptedSolveTime)
			accepted = &value
		}
		row, writeErr := querier.CreateSwissPointLedgerEntry(ctx, sqlc.CreateSwissPointLedgerEntryParams{
			ID:           correctionWorkflowUUID(mutation.Command.CommandID, "swiss-point-"+entry.ParticipantID.String()),
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
			RoundID: entry.RoundID, RoundNumber: roundNumber, SourceKind: string(entry.SourceKind),
			SourceSeriesID: nullableUUIDValue(scope.SeriesID), SeriesResultRevisionID: nullableUUIDValue(wantedRevisionID),
			ResultLabel: optionalTrimmedString(string(entry.ResultLabel)), ParticipantID: entry.ParticipantID,
			OpponentID: nullableUUIDValue(*entry.OpponentID), Points: points,
			EffectiveTimeNs: int64(entry.EffectiveTime), AcceptedSolveTimeNs: accepted,
			StableSeed: int32(entry.StableSeed), CreatedAt: tstz(mutation.Evidence.RequestedAt),
		})
		if writeErr != nil {
			return fmt.Errorf(
				"create Swiss ledger successor for participant %s: %w: %v",
				entry.ParticipantID,
				mapRepositoryWriteError("TournamentAdminCorrectionPostgres - create Swiss ledger successor", writeErr),
				writeErr,
			)
		}
		if row.TournamentID != scope.TournamentID || row.RosterID != scope.RosterID ||
			!row.SourceSeriesID.Valid || row.SourceSeriesID.UUID != scope.SeriesID ||
			!row.SeriesResultRevisionID.Valid || row.SeriesResultRevisionID.UUID != wantedRevisionID ||
			row.ParticipantID != entry.ParticipantID {
			return domain.ErrConflict
		}
		writtenParticipants[entry.ParticipantID] = struct{}{}
	}
	series := mutation.Plan.Series()
	if len(writtenParticipants) != 2 {
		return domain.ErrConflict
	}
	for _, participantID := range []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID} {
		if _, found := writtenParticipants[participantID]; !found {
			return domain.ErrConflict
		}
	}
	return nil
}

func correctionOfficialUUIDPointer(value domain.OfficialResultRevisionID) *uuid.UUID {
	id := value.UUID()
	return &id
}

func (r *TournamentAdminCorrectionPostgres) materializedCorrectionArtifacts(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	scope ResultScope,
) ([]ProjectionArtifactInput, map[domain.ArtifactKind]uuid.UUID, []uuid.UUID, error) {
	participants, err := querier.ListTournamentAdminCorrectionProjectionParticipants(ctx, scope.RosterID)
	if err != nil {
		return nil, nil, nil, tournamentAdminCorrectionError("load projection participants", err)
	}
	ledger, err := querier.ListTournamentAdminCorrectionSwissPointLedger(ctx, sqlc.ListTournamentAdminCorrectionSwissPointLedgerParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return nil, nil, nil, tournamentAdminCorrectionError("load Swiss point ledger", err)
	}
	state, err := correctionCanonicalMaterializedState(scope.TournamentID, participants, ledger)
	if err != nil {
		return nil, nil, nil, err
	}
	state.SwissLedger, err = correctionusecase.ApplyServerOwnedSwissSuccessor(mutation.Plan, state.SwissLedger)
	if err != nil {
		return nil, nil, nil, domain.ErrConflict
	}
	state.SwissComplete = true
	links, err := querier.ListProjectionRevisionArtifacts(ctx, sqlc.ListProjectionRevisionArtifactsParams{
		RevisionID:   mutation.Authority.ProjectionRevisionID,
		TournamentID: scope.TournamentID,
		RosterID:     scope.RosterID,
	})
	if err != nil {
		return nil, nil, nil, tournamentAdminCorrectionError("load current projection artifacts", err)
	}
	currentByKind := make(map[domain.ArtifactKind]uuid.UUID, len(links))
	for _, link := range links {
		kind, valid := correctionArtifactKind(link.ArtifactKind)
		if !valid || kind == domain.ArtifactKindChampion {
			return nil, nil, nil, domain.ErrConflict
		}
		if _, duplicate := currentByKind[kind]; duplicate {
			return nil, nil, nil, domain.ErrConflict
		}
		currentByKind[kind] = link.ArtifactID
	}
	if currentByKind[domain.ArtifactKindStandings] == uuid.Nil {
		return nil, nil, nil, domain.ErrConflict
	}
	state.ArtifactKinds = []domain.ArtifactKind{domain.ArtifactKindStandings}
	for _, kind := range []domain.ArtifactKind{domain.ArtifactKindTopFour, domain.ArtifactKindBracket} {
		if currentByKind[kind] != uuid.Nil || mutation.Stage.CreatePlayoff {
			state.ArtifactKinds = append(state.ArtifactKinds, kind)
		}
	}
	topFour, err := querier.LockTournamentAdminCorrectionTopFour(ctx, sqlc.LockTournamentAdminCorrectionTopFourParams{
		ProjectionRevisionID: mutation.Authority.ProjectionRevisionID,
		TournamentID:         scope.TournamentID,
		RosterID:             scope.RosterID,
	})
	if err != nil {
		return nil, nil, nil, tournamentAdminCorrectionError("load normalized top four", err)
	}
	goldenCommitByPosition := make(map[int]sqlc.LockTournamentAdminCorrectionTopFourRow, len(topFour))
	for _, position := range topFour {
		goldenCommitByPosition[int(position.Position)] = position
	}
	if mutation.Stage.CreatePlayoff {
		settlements, loadErr := querier.LockTournamentProgressionGoldenPositionCommits(
			ctx,
			sqlc.LockTournamentProgressionGoldenPositionCommitsParams{
				TournamentID: scope.TournamentID, RosterID: scope.RosterID,
				SourceProjectionRevisionID: mutation.Authority.ProjectionRevisionID,
				SourceProjectionRevision:   mutation.Authority.ProjectionRevision,
			},
		)
		if loadErr != nil {
			return nil, nil, nil, tournamentAdminCorrectionError("load corrected playoff Golden positions", loadErr)
		}
		for _, settlement := range settlements {
			position := int(settlement.Position)
			if current, duplicate := goldenCommitByPosition[position]; duplicate &&
				(current.ParticipantID != settlement.ParticipantID ||
					!current.GoldenPositionCommitID.Valid ||
					current.GoldenPositionCommitID.UUID != settlement.PositionCommitID) {
				return nil, nil, nil, domain.ErrConflict
			}
			goldenCommitByPosition[position] = sqlc.LockTournamentAdminCorrectionTopFourRow{
				ParticipantID:          settlement.ParticipantID,
				Position:               int32(settlement.Position),
				GoldenPositionCommitID: uuid.NullUUID{UUID: settlement.PositionCommitID, Valid: true},
			}
		}
	}
	rounds, err := projection.BuildCanonicalSwissRounds(state.SwissLedger)
	if err != nil {
		return nil, nil, nil, domain.ErrConflict
	}
	participantIDs := make([]uuid.UUID, len(state.CanonicalParticipants))
	seeds := make([]swissusecase.ParticipantSeed, len(state.CanonicalParticipants))
	for index, participant := range state.CanonicalParticipants {
		participantIDs[index] = participant.ID
		seeds[index] = swissusecase.ParticipantSeed{ParticipantID: participant.ID, Seed: participant.StableSeed}
	}
	standings, err := swissusecase.DeriveRoundStandings(participantIDs, seeds, rounds, true)
	if err != nil || len(standings) < 4 {
		return nil, nil, nil, domain.ErrConflict
	}
	state.TopFour = make([]projection.CanonicalTopFourPosition, 4)
	for index := range state.TopFour {
		state.TopFour[index] = projection.CanonicalTopFourPosition{
			ParticipantID: standings[index].ParticipantID,
			Position:      index + 1,
		}
		current, found := goldenCommitByPosition[index+1]
		if found && current.ParticipantID == standings[index].ParticipantID && current.GoldenPositionCommitID.Valid {
			commitID := current.GoldenPositionCommitID.UUID
			state.TopFour[index].GoldenPositionCommitID = &commitID
		}
	}
	if mutation.Stage.CreatePlayoff {
		state.Bracket = correctionPlayoffBracket(mutation.Command.CommandID, state.TopFour)
	} else if currentByKind[domain.ArtifactKindBracket] != uuid.Nil {
		bracket, loadErr := querier.LockTournamentAdminCorrectionPlayoffBracket(
			ctx,
			sqlc.LockTournamentAdminCorrectionPlayoffBracketParams{
				TournamentID:       scope.TournamentID,
				RosterID:           scope.RosterID,
				TournamentRevision: mutation.Authority.Stage.TournamentRevision,
			},
		)
		if loadErr != nil {
			return nil, nil, nil, tournamentAdminCorrectionError("load normalized playoff bracket", loadErr)
		}
		if len(bracket) == 0 {
			// A native Golden stage has no persisted playoff Series yet. Rebuild
			// its prospective bracket from the corrected Top4 instead of treating
			// the pre-correction projection payload as executable stage authority.
			state.Bracket = correctionPlayoffBracket(mutation.Command.CommandID, state.TopFour)
		} else {
			state.Bracket = make([]projection.CanonicalBracketMatch, len(bracket))
			for index, match := range bracket {
				state.Bracket[index] = projection.CanonicalBracketMatch{
					Position:            int(match.Position),
					SeriesID:            match.SeriesID,
					FirstParticipantID:  match.FirstParticipantID,
					SecondParticipantID: match.SecondParticipantID,
					State:               domain.SeriesState(match.State),
					FirstWins:           int(match.FirstParticipantWins),
					SecondWins:          int(match.SecondParticipantWins),
				}
			}
		}
	}
	materialized, err := correctionusecase.BuildMaterializedProjections(state)
	if err != nil {
		return nil, nil, nil, fmt.Errorf(
			"%w: materialize correction projections: %v (kinds=%v ledger=%d top_four=%d bracket=%d)",
			domain.ErrValidation,
			err,
			state.ArtifactKinds,
			len(state.SwissLedger),
			len(state.TopFour),
			len(state.Bracket),
		)
	}
	if len(materialized.Artifacts) != len(state.ArtifactKinds) {
		return nil, nil, nil, domain.ErrConflict
	}
	affected := make(map[uuid.UUID]struct{}, len(currentByKind))
	for _, artifactID := range currentByKind {
		affected[artifactID] = struct{}{}
	}
	ids := make(map[domain.ArtifactKind]uuid.UUID, len(materialized.Artifacts))
	for _, artifact := range materialized.Artifacts {
		currentID := currentByKind[artifact.Kind]
		if currentID == uuid.Nil && !mutation.Stage.CreatePlayoff {
			return nil, nil, nil, domain.ErrConflict
		}
		if currentID == uuid.Nil {
			ids[artifact.Kind] = correctionWorkflowUUID(mutation.Command.CommandID, "materialized-"+string(artifact.Kind))
		} else if _, changed := affected[currentID]; changed {
			ids[artifact.Kind] = correctionWorkflowUUID(mutation.Command.CommandID, "materialized-"+string(artifact.Kind))
		} else {
			ids[artifact.Kind] = currentID
		}
	}
	standingsID, standingsFound := ids[domain.ArtifactKindStandings]
	topFourID := ids[domain.ArtifactKindTopFour]
	if !standingsFound {
		return nil, nil, nil, domain.ErrConflict
	}
	resultID := mutation.Plan.GameResultRevision().Revision().ID().UUID()
	seriesResultID := mutation.Plan.SeriesResultRevision().Revision().ID().UUID()
	artifacts := make([]ProjectionArtifactInput, 0, len(materialized.Artifacts))
	reused := make([]uuid.UUID, 0, len(materialized.Artifacts))
	for _, artifact := range materialized.Artifacts {
		id := ids[artifact.Kind]
		currentID := currentByKind[artifact.Kind]
		if currentID != uuid.Nil {
			if _, changed := affected[currentID]; !changed {
				reused = append(reused, id)
				continue
			}
		}
		input := ProjectionArtifactInput{
			ID: id, Kind: artifact.Kind, Key: "correction-" + mutation.Command.CommandID.String() + "-" + string(artifact.Kind),
			Payload: correctionJSONRaw(artifact.Payload), PayloadDigest: artifact.PayloadDigest,
			Members: correctionMaterializedMembers(artifact.Members),
		}
		switch artifact.Kind {
		case domain.ArtifactKindStandings:
			input.Dependencies = []ProjectionDependencyInput{
				correctionOfficialResultDependency(mutation.Command.CommandID, artifact.Kind, "game", resultID, scope.SeriesID),
				correctionOfficialResultDependency(mutation.Command.CommandID, artifact.Kind, "series", seriesResultID, scope.SeriesID),
			}
		case domain.ArtifactKindTopFour:
			input.Dependencies = []ProjectionDependencyInput{correctionArtifactDependency(
				mutation.Command.CommandID, artifact.Kind, standingsID,
			)}
			for _, commitID := range artifact.GoldenPositionCommitIDs {
				commitID := commitID
				input.Dependencies = append(input.Dependencies, ProjectionDependencyInput{
					ID: correctionWorkflowUUID(
						mutation.Command.CommandID,
						"dependency-"+string(artifact.Kind)+"-golden-position-"+commitID.String(),
					),
					Kind:                   projectionDependencyGoldenPosition,
					GoldenPositionCommitID: &commitID,
				})
			}
		case domain.ArtifactKindBracket:
			input.Dependencies = []ProjectionDependencyInput{correctionArtifactDependency(
				mutation.Command.CommandID, artifact.Kind, topFourID,
			)}
		default:
			return nil, nil, nil, domain.ErrConflict
		}
		artifacts = append(artifacts, input)
	}
	return artifacts, ids, reused, nil
}

func correctionPlayoffBracket(
	commandID uuid.UUID,
	topFour []projection.CanonicalTopFourPosition,
) []projection.CanonicalBracketMatch {
	if commandID == uuid.Nil || len(topFour) != 4 {
		return nil
	}
	return []projection.CanonicalBracketMatch{
		{
			Position: 1, SeriesID: correctionWorkflowUUID(commandID, "playoff-semifinal-series-1"),
			FirstParticipantID: topFour[0].ParticipantID, SecondParticipantID: topFour[1].ParticipantID,
			State: domain.SeriesStateLocked,
		},
		{
			Position: 2, SeriesID: correctionWorkflowUUID(commandID, "playoff-semifinal-series-2"),
			FirstParticipantID: topFour[2].ParticipantID, SecondParticipantID: topFour[3].ParticipantID,
			State: domain.SeriesStateLocked,
		},
	}
}

func correctionCanonicalMaterializedState(
	tournamentID uuid.UUID,
	participants []sqlc.ListTournamentAdminCorrectionProjectionParticipantsRow,
	ledger []sqlc.ListTournamentAdminCorrectionSwissPointLedgerRow,
) (correctionusecase.MaterializedProjectionState, error) {
	if tournamentID == uuid.Nil || len(participants) == 0 || len(ledger) == 0 {
		return correctionusecase.MaterializedProjectionState{}, domain.ErrConflict
	}
	state := correctionusecase.MaterializedProjectionState{
		TournamentID:          tournamentID,
		CanonicalParticipants: make([]projection.CanonicalSwissParticipant, len(participants)),
		SwissLedger:           make([]projection.CanonicalSwissPointLedgerEntry, len(ledger)),
		ArtifactKinds: []domain.ArtifactKind{
			domain.ArtifactKindStandings,
			domain.ArtifactKindTopFour,
			domain.ArtifactKindBracket,
		},
	}
	for index, participant := range participants {
		if participant.ID == uuid.Nil || participant.Seed < 1 {
			return correctionusecase.MaterializedProjectionState{}, domain.ErrConflict
		}
		state.CanonicalParticipants[index] = projection.CanonicalSwissParticipant{
			ID: participant.ID, StableSeed: int(participant.Seed),
		}
	}
	for index, entry := range ledger {
		canonical, err := correctionCanonicalSwissLedgerEntry(entry)
		if err != nil {
			return correctionusecase.MaterializedProjectionState{}, err
		}
		state.SwissLedger[index] = canonical
	}
	return state, nil
}

func correctionCanonicalSwissLedgerEntry(
	entry sqlc.ListTournamentAdminCorrectionSwissPointLedgerRow,
) (projection.CanonicalSwissPointLedgerEntry, error) {
	if entry.RoundID == uuid.Nil || entry.RoundRevisionID == uuid.Nil || entry.ParticipantID == uuid.Nil ||
		entry.RoundNumber < 1 || entry.StableSeed < 1 || entry.Points < 0 || entry.EffectiveTimeNs < 0 {
		return projection.CanonicalSwissPointLedgerEntry{}, domain.ErrConflict
	}
	canonical := projection.CanonicalSwissPointLedgerEntry{
		RoundID: entry.RoundID, RoundRevisionID: entry.RoundRevisionID, RoundNumber: int(entry.RoundNumber),
		SourceKind: swissusecase.PointSourceKind(entry.SourceKind), ResultLabel: swissusecase.SeriesResultLabel(stringValue(entry.ResultLabel)),
		ParticipantID: entry.ParticipantID, Points: int(entry.Points), EffectiveTime: time.Duration(entry.EffectiveTimeNs),
		StableSeed: int(entry.StableSeed),
	}
	if entry.SourceSeriesID.Valid {
		canonical.SourceSeriesID = entry.SourceSeriesID.UUID
	}
	if entry.SeriesResultRevisionID.Valid {
		canonical.SeriesResultRevisionID = entry.SeriesResultRevisionID.UUID
	}
	if entry.ByeRevisionID.Valid {
		canonical.ByeRevisionID = entry.ByeRevisionID.UUID
	}
	if entry.OpponentID.Valid {
		opponentID := entry.OpponentID.UUID
		canonical.OpponentID = &opponentID
	}
	if entry.AcceptedSolveTimeNs != nil {
		accepted := time.Duration(*entry.AcceptedSolveTimeNs)
		canonical.AcceptedSolveTime = &accepted
	}
	return canonical, nil
}

func correctionMaterializedMembers(
	members []correctionusecase.MaterializedProjectionMember,
) []ProjectionMemberInput {
	result := make([]ProjectionMemberInput, len(members))
	for index, member := range members {
		result[index] = ProjectionMemberInput{
			ParticipantID: member.ParticipantID, Position: int32(member.Position), ScoreMilli: member.ScoreMilli,
		}
	}
	return result
}

func correctionOfficialResultDependency(
	commandID uuid.UUID,
	kind domain.ArtifactKind,
	role string,
	revisionID, seriesID uuid.UUID,
) ProjectionDependencyInput {
	return ProjectionDependencyInput{
		ID: correctionWorkflowUUID(commandID, "dependency-"+string(kind)+"-"+role), Kind: projectionDependencyOfficialResult,
		OfficialResultRevisionID: correctionPointer(revisionID), OfficialResultSeriesID: correctionPointer(seriesID),
	}
}

func correctionArtifactDependency(
	commandID uuid.UUID,
	kind domain.ArtifactKind,
	dependsOnID uuid.UUID,
) ProjectionDependencyInput {
	return ProjectionDependencyInput{
		ID: correctionWorkflowUUID(commandID, "dependency-"+string(kind)), Kind: projectionDependencyArtifact,
		DependsOnArtifactID: correctionPointer(dependsOnID),
	}
}

func correctionLogicalBindings(
	mutation tournamentadmin.CorrectionMutation,
	scope ResultScope,
	artifacts map[domain.ArtifactKind]uuid.UUID,
) (tournamentAdminCorrectionLogicalPlan, error) {
	successors := make(map[domain.ArtifactRef]domain.ProjectionRevision, len(mutation.Plan.ProjectionRevisions()))
	for _, projection := range mutation.Plan.ProjectionRevisions() {
		successors[projection.Revision().Artifact()] = projection
	}
	want := []struct {
		kind     domain.ArtifactKind
		entityID uuid.UUID
		sourceID uuid.UUID
	}{
		{domain.ArtifactKindGameResult, scope.AttemptID, mutation.Plan.GameResultRevision().Revision().ID().UUID()},
		{domain.ArtifactKindSeriesScore, scope.SeriesID, mutation.Plan.ScoreRevision().Revision().ID().UUID()},
		{domain.ArtifactKindSeriesResult, scope.SeriesID, mutation.Plan.SeriesResultRevision().Revision().ID().UUID()},
	}
	for _, kind := range []domain.ArtifactKind{
		domain.ArtifactKindStandings, domain.ArtifactKindTopFour, domain.ArtifactKindBracket,
	} {
		if sourceID := artifacts[kind]; sourceID != uuid.Nil {
			want = append(want, struct {
				kind     domain.ArtifactKind
				entityID uuid.UUID
				sourceID uuid.UUID
			}{kind: kind, entityID: scope.TournamentID, sourceID: sourceID})
		}
	}
	bindings := make([]tournamentAdminCorrectionBinding, 0, len(want))
	for _, item := range want {
		next, found := successors[domain.ArtifactRef{Kind: item.kind, EntityID: item.entityID}]
		if !found && mutation.Stage.CreatePlayoff &&
			(item.kind == domain.ArtifactKindTopFour || item.kind == domain.ArtifactKindBracket) {
			continue
		}
		if !found || item.sourceID == uuid.Nil {
			return tournamentAdminCorrectionLogicalPlan{}, domain.ErrConflict
		}
		bindings = append(bindings, tournamentAdminCorrectionBinding{
			kind: item.kind, entityID: item.entityID, sourceID: item.sourceID, nodeID: next.Revision().ID().UUID(),
		})
	}
	return tournamentAdminCorrectionLogicalPlan{bindings: bindings}, nil
}

func correctionResultArtifactKind(kind domain.ArtifactKind) bool {
	switch kind {
	case domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore, domain.ArtifactKindSeriesResult:
		return true
	default:
		return false
	}
}

func persistTournamentAdminCorrectionLogicalPlan(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	logical tournamentAdminCorrectionLogicalPlan,
) error {
	authorityID := correctionWorkflowUUID(mutation.Command.CommandID, "result-projection-authority")
	if err := querier.CreateResultProjectionNodeAuthority(ctx, sqlc.CreateResultProjectionNodeAuthorityParams{
		ID: authorityID, TournamentID: mutation.Command.TournamentID, RosterID: mutation.Authority.RosterID,
		SourceKind: "correction_commit", CorrectionCommandID: nullableUUIDValue(mutation.Command.CommandID),
		CreatedAt: tstz(mutation.Evidence.RequestedAt),
	}); err != nil {
		return tournamentAdminCorrectionError("persist projection authority", err)
	}
	snapshot := mutation.Plan.DAGSnapshot()
	projections := append([]domain.ProjectionRevision(nil), snapshot.Projections...)
	sortCorrectionProjections(projections)
	planned := make(map[domain.DerivedRevisionID]struct{}, len(mutation.Plan.ProjectionRevisions()))
	for _, projection := range mutation.Plan.ProjectionRevisions() {
		planned[projection.Revision().ID()] = struct{}{}
	}
	type persistedProjectionNode struct {
		id        uuid.UUID
		revision  int64
		authority uuid.UUID
	}
	persisted := make(map[domain.DerivedRevisionID]persistedProjectionNode, len(projections))
	owned := make(map[uuid.UUID]struct{}, len(planned))
	var playoffIDs tournamentprogression.PlayoffPublicationIDs
	if mutation.Stage.CreatePlayoff {
		var identityErr error
		playoffIDs, identityErr = tournamentprogression.PlayoffPublicationIdentity(mutation.Command.CommandID)
		if identityErr != nil {
			return identityErr
		}
	}
	for _, projection := range projections {
		revision := projection.Revision()
		if mutation.Stage.CreatePlayoff && revision.Artifact().EntityID == mutation.Command.TournamentID {
			var stageNodeID uuid.UUID
			switch revision.Artifact().Kind {
			case domain.ArtifactKindTopFour:
				stageNodeID = playoffIDs.Top4NodeID
			case domain.ArtifactKindBracket:
				stageNodeID = playoffIDs.BracketNodeID
			}
			if stageNodeID != uuid.Nil {
				stored, lookupErr := querier.GetResultProjectionNode(ctx, stageNodeID)
				if lookupErr != nil {
					return tournamentAdminCorrectionError("load corrected playoff stage node", lookupErr)
				}
				if stored.ID != stageNodeID || stored.AuthorityID != playoffIDs.StageNodeAuthorityID ||
					stored.TournamentID != mutation.Command.TournamentID || stored.RosterID != mutation.Authority.RosterID ||
					stored.ArtifactKind != string(revision.Artifact().Kind) ||
					stored.EntityID != revision.Artifact().EntityID || stored.RevisionNumber < 1 {
					return domain.ErrConflict
				}
				persisted[revision.ID()] = persistedProjectionNode{
					id: stageNodeID, revision: stored.RevisionNumber, authority: playoffIDs.StageNodeAuthorityID,
				}
				continue
			}
		}
		_, successor := planned[revision.ID()]
		if !successor && correctionResultArtifactKind(revision.Artifact().Kind) {
			sourceID := correctionAuthorityResultSourceID(mutation.Authority.Core, revision.Artifact().Kind)
			existing, lookupErr := querier.GetCorrectionResultProjectionNodeForSource(
				ctx,
				sqlc.GetCorrectionResultProjectionNodeForSourceParams{
					TournamentID: revision.TournamentID(), RosterID: mutation.Authority.RosterID,
					ArtifactKind: correctionArtifactKindSQL(revision.Artifact().Kind),
					EntityID:     revision.Artifact().EntityID, SourceID: sourceID,
				},
			)
			if lookupErr != nil {
				if errors.Is(lookupErr, pgx.ErrNoRows) {
					return fmt.Errorf("normalized result projection node %s/%s source %s is missing: %w",
						revision.Artifact().Kind, revision.Artifact().EntityID, sourceID, domain.ErrConflict)
				}
				return tournamentAdminCorrectionError("load normalized result projection node", lookupErr)
			}
			if existing.TournamentID != revision.TournamentID() || existing.RosterID != mutation.Authority.RosterID ||
				existing.ArtifactKind != correctionArtifactKindSQL(revision.Artifact().Kind) ||
				existing.EntityID != revision.Artifact().EntityID || existing.RevisionNumber < 1 {
				return fmt.Errorf("normalized result projection node does not match %s/%s source %s: %w",
					revision.Artifact().Kind, revision.Artifact().EntityID, sourceID, domain.ErrConflict)
			}
			persisted[revision.ID()] = persistedProjectionNode{
				id: existing.ID, revision: existing.RevisionNumber, authority: existing.AuthorityID,
			}
			continue
		}
		if successor {
			parent := revision.PreviousRevisionID()
			if parent == nil {
				return fmt.Errorf("correction projection successor %s has no predecessor: %w", revision.ID().UUID(), domain.ErrConflict)
			}
			previous, found := persisted[*parent]
			if !found || previous.id == uuid.Nil || previous.revision < 1 {
				return fmt.Errorf(
					"correction projection predecessor %s for %s/%s is not persisted: %w",
					parent.UUID(), revision.Artifact().Kind, revision.Artifact().EntityID, domain.ErrConflict,
				)
			}
			storedRevision := previous.revision + 1
			existing, lookupErr := querier.GetCorrectionProjectionNodeByRevision(
				ctx,
				sqlc.GetCorrectionProjectionNodeByRevisionParams{
					TournamentID: revision.TournamentID(), RosterID: mutation.Authority.RosterID,
					ArtifactKind: correctionArtifactKindSQL(revision.Artifact().Kind), EntityID: revision.Artifact().EntityID,
					RevisionNumber: storedRevision,
				},
			)
			if lookupErr == nil {
				if existing.ID != revision.ID().UUID() || !existing.PreviousNodeID.Valid || existing.PreviousNodeID.UUID != previous.id {
					return fmt.Errorf(
						"correction projection successor identity %s/%s revision %d planned %s already owned by %s: %w",
						revision.Artifact().Kind, revision.Artifact().EntityID, storedRevision,
						revision.ID().UUID(), existing.ID, domain.ErrConflict,
					)
				}
				persisted[revision.ID()] = persistedProjectionNode{
					id: existing.ID, revision: existing.RevisionNumber, authority: existing.AuthorityID,
				}
				continue
			}
			if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return tournamentAdminCorrectionError("load projection successor identity", lookupErr)
			}
			if err := querier.CreateResultProjectionNode(ctx, sqlc.CreateResultProjectionNodeParams{
				ID: revision.ID().UUID(), AuthorityID: authorityID, TournamentID: revision.TournamentID(),
				RosterID: mutation.Authority.RosterID, ArtifactKind: correctionArtifactKindSQL(revision.Artifact().Kind),
				EntityID: revision.Artifact().EntityID, RevisionNumber: storedRevision,
				PreviousNodeID: nullableUUIDValue(previous.id), Payload: projection.Payload(),
				PayloadDigest: correctionDigestBytes(revision.PayloadDigest()), CreatedAt: tstz(revision.CreatedAt()),
			}); err != nil {
				return fmt.Errorf(
					"TournamentAdminCorrectionPostgres - persist projection node %s/%s revision %d: %w",
					revision.Artifact().Kind, revision.Artifact().EntityID, storedRevision, err,
				)
			}
			persisted[revision.ID()] = persistedProjectionNode{id: revision.ID().UUID(), revision: storedRevision, authority: authorityID}
			owned[revision.ID().UUID()] = struct{}{}
			continue
		}
		existing, lookupErr := querier.GetCorrectionProjectionNodeByRevision(
			ctx,
			sqlc.GetCorrectionProjectionNodeByRevisionParams{
				TournamentID:   revision.TournamentID(),
				RosterID:       mutation.Authority.RosterID,
				ArtifactKind:   correctionArtifactKindSQL(revision.Artifact().Kind),
				EntityID:       revision.Artifact().EntityID,
				RevisionNumber: int64(revision.RevisionNo()),
			},
		)
		if lookupErr == nil {
			persisted[revision.ID()] = persistedProjectionNode{
				id: existing.ID, revision: existing.RevisionNumber, authority: existing.AuthorityID,
			}
			continue
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return tournamentAdminCorrectionError("load projection node identity", lookupErr)
		}
		previous := uuid.NullUUID{}
		if parent := revision.PreviousRevisionID(); parent != nil {
			parentNode, found := persisted[*parent]
			if !found || parentNode.id == uuid.Nil {
				return fmt.Errorf(
					"correction projection predecessor %s for %s/%s revision %d is not persisted: %w",
					parent.UUID(),
					revision.Artifact().Kind,
					revision.Artifact().EntityID,
					revision.RevisionNo(),
					domain.ErrConflict,
				)
			}
			previous = nullableUUIDValue(parentNode.id)
		}
		if err := querier.CreateResultProjectionNode(ctx, sqlc.CreateResultProjectionNodeParams{
			ID: revision.ID().UUID(), AuthorityID: authorityID, TournamentID: revision.TournamentID(),
			RosterID: mutation.Authority.RosterID, ArtifactKind: correctionArtifactKindSQL(revision.Artifact().Kind),
			EntityID: revision.Artifact().EntityID, RevisionNumber: int64(revision.RevisionNo()), PreviousNodeID: previous,
			Payload: projection.Payload(), PayloadDigest: correctionDigestBytes(revision.PayloadDigest()), CreatedAt: tstz(revision.CreatedAt()),
		}); err != nil {
			return fmt.Errorf(
				"TournamentAdminCorrectionPostgres - persist projection node %s/%s revision %d: %w",
				revision.Artifact().Kind,
				revision.Artifact().EntityID,
				revision.RevisionNo(),
				err,
			)
		}
		persisted[revision.ID()] = persistedProjectionNode{id: revision.ID().UUID(), revision: int64(revision.RevisionNo()), authority: authorityID}
		owned[revision.ID().UUID()] = struct{}{}
	}
	for _, dependency := range snapshot.Dependencies {
		source, sourceFound := persisted[dependency.SourceRevisionID]
		derived, derivedFound := persisted[dependency.DerivedRevisionID]
		if !sourceFound || !derivedFound {
			return fmt.Errorf(
				"correction dependency %s -> %s persisted source=%t derived=%t: %w",
				dependency.SourceRevisionID.UUID(), dependency.DerivedRevisionID.UUID(), sourceFound, derivedFound, domain.ErrConflict,
			)
		}
		if _, sourceCreated := owned[source.id]; !sourceCreated {
			continue
		}
		if _, derivedCreated := owned[derived.id]; !derivedCreated {
			continue
		}
		if err := querier.CreateResultProjectionDependency(ctx, sqlc.CreateResultProjectionDependencyParams{
			AuthorityID: authorityID, SourceNodeID: source.id,
			DerivedNodeID: derived.id, CreatedAt: tstz(mutation.Evidence.RequestedAt),
		}); err != nil {
			return tournamentAdminCorrectionError("persist projection dependency", err)
		}
	}
	for _, decision := range mutation.Plan.Decisions() {
		node, found := persisted[decision.ProjectionRevisionID]
		if !found || node.id == uuid.Nil {
			return fmt.Errorf("correction decision projection %s is not persisted: %w", decision.ProjectionRevisionID.UUID(), domain.ErrConflict)
		}
		if err := querier.CreateCorrectionProjectionDecision(ctx, sqlc.CreateCorrectionProjectionDecisionParams{
			ID: decision.ID, CommandID: mutation.Command.CommandID, SequenceNumber: int32(decision.Sequence),
			ProjectionNodeID: node.id, Payload: decision.Payload,
			PayloadDigest: correctionDigestBytes(decision.PayloadDigest), RecordedAt: tstz(decision.RecordedAt),
			CreatedAt: tstz(mutation.Evidence.RequestedAt),
		}); err != nil {
			return tournamentAdminCorrectionError("persist projection decision", err)
		}
	}
	for _, binding := range logical.bindings {
		node, found := persisted[domain.DerivedRevisionID(binding.nodeID)]
		if !found || node.id == uuid.Nil {
			return fmt.Errorf("correction binding projection %s is not persisted: %w", binding.nodeID, domain.ErrConflict)
		}
		if err := querier.CreateCorrectionProjectionBinding(ctx, sqlc.CreateCorrectionProjectionBindingParams{
			CommandID: mutation.Command.CommandID, TournamentID: mutation.Command.TournamentID, RosterID: mutation.Authority.RosterID,
			ArtifactKind: correctionArtifactKindSQL(binding.kind), EntityID: binding.entityID, SourceID: binding.sourceID,
			NodeID: node.id, CreatedAt: tstz(mutation.Evidence.RequestedAt),
		}); err != nil {
			return tournamentAdminCorrectionError("persist projection binding", err)
		}
	}
	return nil
}

func correctionAuthorityResultSourceID(authority correctionusecase.Authority, kind domain.ArtifactKind) uuid.UUID {
	switch kind {
	case domain.ArtifactKindGameResult:
		return authority.GameResult.ID.UUID()
	case domain.ArtifactKindSeriesScore:
		return authority.Score.ID.UUID()
	case domain.ArtifactKindSeriesResult:
		return authority.SeriesResult.ID.UUID()
	default:
		return uuid.Nil
	}
}

func persistTournamentAdminCorrectionCommand(
	ctx context.Context,
	querier *sqlc.Queries,
	mutation tournamentadmin.CorrectionMutation,
	input CorrectionInput,
) error {
	planBytes := mutation.Plan.Bytes()
	planDigest := sha256.Sum256(planBytes)
	evidenceBytes, err := marshalTournamentAdminCorrectionEvidence(mutation.Evidence)
	if err != nil {
		return err
	}
	row, err := querier.CreateTournamentAdminCorrectionCommand(ctx, sqlc.CreateTournamentAdminCorrectionCommandParams{
		CommandID: mutation.Command.CommandID, TournamentID: mutation.Command.TournamentID, RosterID: mutation.Authority.RosterID,
		SeriesID: mutation.Command.SeriesID, GameAttemptID: mutation.Command.GameID, ActorID: mutation.Command.Operator.ActorID,
		SourceProjectionRevisionID: mutation.Authority.ProjectionRevisionID, SourceProjectionRevision: mutation.Authority.ProjectionRevision,
		ResultingProjectionRevisionID: input.ProjectionIDs.RevisionID, ResultingProjectionRevision: mutation.Authority.ProjectionRevision + 1,
		RequestDigest: correctionDigestBytes(mutation.RequestDigest), PlanDigest: correctionDigestBytes(planDigest),
		ValidationDigest: correctionDigestBytes(mutation.Evidence.ValidationDigest), PlanDocument: planBytes, EvidenceDocument: evidenceBytes,
		ResultCommitID: input.IDs.CommitID, ExecutedAt: tstz(mutation.Evidence.RequestedAt),
	})
	if err != nil {
		return tournamentAdminCorrectionError("persist correction command", err)
	}
	if row.CommandID != mutation.Command.CommandID || row.ResultCommitID != input.IDs.CommitID ||
		!correctionBytesEqual(row.RequestDigest, mutation.RequestDigest[:]) ||
		!correctionBytesEqual(row.PlanDigest, planDigest[:]) ||
		!correctionBytesEqual(row.ValidationDigest, mutation.Evidence.ValidationDigest[:]) {
		return domain.ErrConflict
	}
	consistency, err := querier.GetTournamentAdminCorrectionConsistency(ctx, mutation.Command.CommandID)
	if err != nil {
		return tournamentAdminCorrectionError("validate correction consistency", err)
	}
	wantConsistency := sqlc.GetTournamentAdminCorrectionConsistencyRow{
		TournamentMatches: true, RosterMatches: true, SeriesMatches: true, GameMatches: true,
		CommandMatches: true, ActorKindMatches: true, ActorMatches: true, PlanSchemaMatches: true,
		EvidenceSchemaMatches: true, EvidenceCommandMatches: true, EvidenceTournamentMatches: true,
		EvidenceSeriesMatches: true, EvidenceGameMatches: true, EvidenceActorMatches: true, ValidationMatches: true,
	}
	if consistency != wantConsistency {
		return fmt.Errorf("correction atomic consistency mismatch %+v: %w", consistency, domain.ErrConflict)
	}
	return nil
}

type tournamentAdminCorrectionEvidenceDocument struct {
	Schema           string                                       `json:"schema"`
	CommandID        uuid.UUID                                    `json:"command_id"`
	TournamentID     uuid.UUID                                    `json:"tournament_id"`
	SeriesID         uuid.UUID                                    `json:"series_id"`
	GameID           uuid.UUID                                    `json:"game_id"`
	OperatorID       uuid.UUID                                    `json:"operator_id"`
	Reason           string                                       `json:"reason"`
	Fields           []string                                     `json:"fields"`
	RequestedAt      time.Time                                    `json:"requested_at"`
	ValidationDigest string                                       `json:"validation_digest"`
	Supersessions    []tournamentadmin.ProjectionSupersessionView `json:"supersessions"`
	UnlockIntents    []tournamentadmin.CorrectionUnlockIntent     `json:"unlock_intents"`
}

func marshalTournamentAdminCorrectionEvidence(
	evidence tournamentadmin.CorrectionEvidence,
) ([]byte, error) {
	if !validTournamentAdminCorrectionEvidence(evidence) {
		return nil, domain.ErrValidation
	}
	document := tournamentAdminCorrectionEvidenceDocument{
		Schema: "result-correction-evidence-v1", CommandID: evidence.CommandID, TournamentID: evidence.TournamentID,
		SeriesID: evidence.SeriesID, GameID: evidence.GameID, OperatorID: evidence.OperatorID, Reason: evidence.Reason,
		Fields: append([]string(nil), evidence.Fields...), RequestedAt: correctionTime(evidence.RequestedAt),
		ValidationDigest: hex.EncodeToString(evidence.ValidationDigest[:]),
		Supersessions:    append([]tournamentadmin.ProjectionSupersessionView(nil), evidence.Supersessions...),
		UnlockIntents:    append(make([]tournamentadmin.CorrectionUnlockIntent, 0, len(evidence.UnlockIntents)), evidence.UnlockIntents...),
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminCorrectionPostgres - marshal evidence: %w", err)
	}
	return payload, nil
}

func tournamentAdminCorrectionCommandRecord(
	row sqlc.ResultCorrectionCommit,
) (*tournamentadmin.CorrectionCommandRecord, error) {
	if row.CommandID == uuid.Nil || row.TournamentID == uuid.Nil || row.RosterID == uuid.Nil || row.SeriesID == uuid.Nil ||
		row.GameAttemptID == uuid.Nil || row.ActorID == uuid.Nil || row.SourceProjectionRevision < 1 || !row.ExecutedAt.Valid {
		return nil, domain.ErrConflict
	}
	requestDigest, requestValid := correctionDigestFromBytes(row.RequestDigest)
	validationDigest, validationValid := correctionDigestFromBytes(row.ValidationDigest)
	if !requestValid || !validationValid {
		return nil, domain.ErrConflict
	}
	var document tournamentAdminCorrectionEvidenceDocument
	if err := json.Unmarshal(row.EvidenceDocument, &document); err != nil {
		return nil, domain.ErrConflict
	}
	if document.Schema != "result-correction-evidence-v1" || document.CommandID != row.CommandID ||
		document.TournamentID != row.TournamentID || document.SeriesID != row.SeriesID || document.GameID != row.GameAttemptID ||
		document.OperatorID != row.ActorID || document.ValidationDigest != hex.EncodeToString(validationDigest[:]) ||
		!document.RequestedAt.Equal(correctionTime(row.ExecutedAt.Time)) {
		return nil, domain.ErrConflict
	}
	evidence := tournamentadmin.CorrectionEvidence{
		CommandID: document.CommandID, TournamentID: document.TournamentID, SeriesID: document.SeriesID, GameID: document.GameID,
		OperatorID: document.OperatorID, Reason: document.Reason, Fields: append([]string(nil), document.Fields...),
		RequestedAt: correctionTime(document.RequestedAt), ValidationDigest: validationDigest,
		Supersessions: append([]tournamentadmin.ProjectionSupersessionView(nil), document.Supersessions...),
		UnlockIntents: append(make([]tournamentadmin.CorrectionUnlockIntent, 0, len(document.UnlockIntents)), document.UnlockIntents...),
	}
	if !validTournamentAdminCorrectionEvidence(evidence) {
		return nil, domain.ErrConflict
	}
	return &tournamentadmin.CorrectionCommandRecord{
		CommandID: row.CommandID, TournamentID: row.TournamentID, RosterID: row.RosterID, SeriesID: row.SeriesID,
		GameID: row.GameAttemptID, OperatorID: row.ActorID, ExpectedProjectionRevision: row.SourceProjectionRevision,
		RequestDigest: requestDigest, Evidence: evidence, ExecutedAt: correctionTime(row.ExecutedAt.Time),
	}, nil
}

func validTournamentAdminCorrectionEvidence(value tournamentadmin.CorrectionEvidence) bool {
	return value.CommandID != uuid.Nil && value.TournamentID != uuid.Nil && value.SeriesID != uuid.Nil &&
		value.GameID != uuid.Nil && value.OperatorID != uuid.Nil && value.Reason != "" &&
		validServerTime(correctionTime(value.RequestedAt)) && value.ValidationDigest != ([sha256.Size]byte{})
}

func correctionCommitNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
