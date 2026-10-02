package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/publication"
)

var _ projection.FinalPublicationRepository = (*ProjectionPostgres)(nil)

func (r *ProjectionPostgres) PublishFinal(
	ctx context.Context,
	publication projection.FinalPublication,
) (projection.FinalPublicationReceipt, error) {
	publication = publication.Snapshot()
	if r == nil || r.tx == nil || publication.Validate() != nil {
		return projection.FinalPublicationReceipt{}, domain.ErrValidation
	}

	var receipt projection.FinalPublicationReceipt
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		receipt, err = r.publishFinalLocked(txCtx, publication)
		return err
	})
	if err != nil {
		return projection.FinalPublicationReceipt{}, err
	}
	return receipt, nil
}

func (r *ProjectionPostgres) publishFinalLocked(
	ctx context.Context,
	publication projection.FinalPublication,
) (projection.FinalPublicationReceipt, error) {
	authority, err := r.lockFinalProjectionAuthority(ctx, publication)
	if err != nil {
		return projection.FinalPublicationReceipt{}, err
	}
	currentRevision, currentRevisionID := finalProjectionCurrent(authority)
	replay := currentRevisionID == publication.IDs.RevisionID
	if conflict := finalProjectionRevisionConflict(
		publication,
		authority.aggregate,
		currentRevision,
		replay,
	); conflict != nil {
		return projection.FinalPublicationReceipt{}, finalProjectionSourceConflict(publication, authority)
	}
	if err := validateFinalProjectionAuthority(
		publication,
		authority.aggregate,
		authority.attempts,
		authority.seriesHead,
		authority.scoreHead,
		authority.commits,
		authority.terminalCommit,
	); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return projection.FinalPublicationReceipt{}, finalProjectionSourceConflict(publication, authority)
		}
		return projection.FinalPublicationReceipt{}, err
	}
	if replay {
		championEvent, championErr := r.createFinalChampionOutboxEvent(ctx, publication, currentRevision)
		if championErr != nil {
			return projection.FinalPublicationReceipt{}, championErr
		}
		return projection.FinalPublicationReceipt{
			ProjectionRevisionID: publication.IDs.RevisionID,
			ProjectionRevision:   currentRevision,
			TournamentRevision:   authority.aggregate.TournamentRevision,
			OutboxEventID:        championEvent.ID,
			OutboxOrdinal:        championEvent.ProjectionOrdinal,
		}, nil
	}
	return r.persistFinalProjection(ctx, publication, authority, currentRevision)
}

type finalProjectionAuthority struct {
	aggregate      sqlc.LockFinalProjectionAggregateRow
	attempts       []sqlc.LockFinalProjectionAttemptsRow
	seriesHead     sqlc.LockFinalProjectionSeriesResultHeadRow
	scoreHead      sqlc.LockFinalProjectionScoreHeadRow
	commits        []sqlc.LockFinalProjectionResultCommitsRow
	terminalCommit sqlc.LockFinalProjectionResultCommitsRow
	current        sqlc.ProjectionRevision
	currentFound   bool
}

func (r *ProjectionPostgres) lockFinalProjectionAuthority(
	ctx context.Context,
	publication projection.FinalPublication,
) (finalProjectionAuthority, error) {
	querier := r.tx.Querier(ctx)
	aggregate, err := querier.LockFinalProjectionAggregate(ctx, sqlc.LockFinalProjectionAggregateParams{
		TournamentID: publication.Scope.TournamentID,
		RosterID:     publication.Scope.RosterID,
		SeriesID:     publication.Scope.SeriesID,
	})
	if err != nil {
		return finalProjectionAuthority{}, projectionLookupError("PublishFinal - lock aggregate", err)
	}
	attempts, err := querier.LockFinalProjectionAttempts(ctx, sqlc.LockFinalProjectionAttemptsParams{
		SeriesID: publication.Scope.SeriesID,
		RosterID: publication.Scope.RosterID,
	})
	if err != nil {
		return finalProjectionAuthority{}, fmt.Errorf("ProjectionPostgres - PublishFinal - lock attempts: %w", err)
	}
	seriesHead, err := querier.LockFinalProjectionSeriesResultHead(
		ctx,
		sqlc.LockFinalProjectionSeriesResultHeadParams{
			SeriesID: publication.Scope.SeriesID,
			RosterID: publication.Scope.RosterID,
		},
	)
	if err != nil {
		return finalProjectionAuthority{}, projectionLookupError("PublishFinal - lock Series result head", err)
	}
	scoreHead, err := querier.LockFinalProjectionScoreHead(ctx, sqlc.LockFinalProjectionScoreHeadParams{
		SeriesID: publication.Scope.SeriesID,
		RosterID: publication.Scope.RosterID,
	})
	if err != nil {
		return finalProjectionAuthority{}, projectionLookupError("PublishFinal - lock score head", err)
	}
	if _, err = querier.LockProjectionRevisionSet(ctx, sqlc.LockProjectionRevisionSetParams{
		TournamentID: publication.Scope.TournamentID,
		RosterID:     publication.Scope.RosterID,
	}); err != nil {
		return finalProjectionAuthority{}, fmt.Errorf("ProjectionPostgres - PublishFinal - lock revisions: %w", err)
	}
	current, currentErr := querier.GetCurrentProjectionRevision(
		ctx,
		sqlc.GetCurrentProjectionRevisionParams{
			TournamentID: publication.Scope.TournamentID,
			RosterID:     publication.Scope.RosterID,
		},
	)
	if currentErr != nil && !errors.Is(currentErr, pgx.ErrNoRows) {
		return finalProjectionAuthority{}, fmt.Errorf("ProjectionPostgres - PublishFinal - current revision: %w", currentErr)
	}
	commits, err := querier.LockFinalProjectionResultCommits(ctx, sqlc.LockFinalProjectionResultCommitsParams{
		TournamentID: publication.Scope.TournamentID,
		RosterID:     publication.Scope.RosterID,
		SeriesID:     publication.Scope.SeriesID,
	})
	if err != nil {
		return finalProjectionAuthority{}, fmt.Errorf("ProjectionPostgres - PublishFinal - lock result commits: %w", err)
	}
	terminalCommit, _ := finalProjectionTerminalCommit(publication, commits)
	return finalProjectionAuthority{
		aggregate:      aggregate,
		attempts:       attempts,
		seriesHead:     seriesHead,
		scoreHead:      scoreHead,
		commits:        commits,
		terminalCommit: terminalCommit,
		current:        current,
		currentFound:   currentErr == nil,
	}, nil
}

func finalProjectionTerminalCommit(
	publication projection.FinalPublication,
	commits []sqlc.LockFinalProjectionResultCommitsRow,
) (sqlc.LockFinalProjectionResultCommitsRow, bool) {
	var terminal sqlc.LockFinalProjectionResultCommitsRow
	found := false
	for _, commit := range commits {
		matches := allFinalProjectionChecks(
			commit.AttemptID == publication.Scope.GameAttemptID,
			commit.GameResultRevisionID == publication.Expected.GameResultRevisionID.UUID(),
			commit.SeriesScoreRevisionID == publication.Expected.ScoreRevisionID.UUID(),
			commit.SeriesResultRevisionID.Valid,
			commit.SeriesResultRevisionID.UUID == publication.Expected.SeriesResultRevisionID.UUID(),
		)
		if matches {
			if found {
				return sqlc.LockFinalProjectionResultCommitsRow{}, false
			}
			terminal = commit
			found = true
		}
	}
	return terminal, found
}

func finalProjectionCurrent(authority finalProjectionAuthority) (int64, uuid.UUID) {
	currentRevision := int64(0)
	currentRevisionID := uuid.Nil
	if authority.currentFound {
		currentRevision = authority.current.RevisionNumber
		currentRevisionID = authority.current.ID
	}
	return currentRevision, currentRevisionID
}

func (r *ProjectionPostgres) persistFinalProjection(
	ctx context.Context,
	publication projection.FinalPublication,
	authority finalProjectionAuthority,
	currentRevision int64,
) (projection.FinalPublicationReceipt, error) {
	querier := r.tx.Querier(ctx)
	projectionInput := finalProjectionPublishInput(publication)
	previousRevisionID := uuid.NullUUID{}
	previousCutoffID := uuid.NullUUID{}
	if authority.currentFound {
		previousRevisionID = nullableUUIDValue(authority.current.ID)
		previousCutoffID = nullableUUIDValue(authority.current.CutoffID)
	}
	if err := createProjectionDraft(
		ctx,
		querier,
		projectionInput,
		currentRevision+1,
		previousRevisionID,
		previousCutoffID,
	); err != nil {
		return projection.FinalPublicationReceipt{}, err
	}
	if authority.currentFound {
		if _, err := querier.SupersedeProjectionRevisionCAS(ctx, sqlc.SupersedeProjectionRevisionCASParams{
			SupersededByRevisionID: nullableUUIDValue(publication.IDs.RevisionID),
			SupersededAt:           tstz(publication.PublishedAt),
			SupersessionReason:     optionalTrimmedString(publication.SupersessionReason),
			ID:                     authority.current.ID,
			TournamentID:           publication.Scope.TournamentID,
			RosterID:               publication.Scope.RosterID,
		}); err != nil {
			return projection.FinalPublicationReceipt{}, projectionCASWriteError("supersede current revision", err)
		}
	}
	if _, err := querier.PublishProjectionRevisionCAS(ctx, sqlc.PublishProjectionRevisionCASParams{
		PublishedAt:  tstz(publication.PublishedAt),
		ID:           publication.IDs.RevisionID,
		TournamentID: publication.Scope.TournamentID,
		RosterID:     publication.Scope.RosterID,
	}); err != nil {
		return projection.FinalPublicationReceipt{}, projectionCASWriteError("publish final revision", err)
	}
	completed, err := querier.CompleteTournamentFromFinalProjectionCAS(
		ctx,
		sqlc.CompleteTournamentFromFinalProjectionCASParams{
			CompletedAt:      tstz(publication.PublishedAt),
			TournamentID:     publication.Scope.TournamentID,
			ExpectedRevision: publication.Expected.TournamentRevision,
		},
	)
	if err != nil {
		return projection.FinalPublicationReceipt{}, projectionCASWriteError("complete Tournament", err)
	}
	// Final publication completes the tournament directly, without the generic
	// lifecycle transition. Release its live participation locks atomically so
	// players can join a new tournament while historical evidence stays intact.
	if _, err = querier.ReleaseTournamentReservations(ctx, publication.Scope.TournamentID); err != nil {
		return projection.FinalPublicationReceipt{}, projectionCASWriteError("release participant reservations", err)
	}
	championEvent, err := r.createFinalChampionOutboxEvent(ctx, publication, currentRevision+1)
	if err != nil {
		return projection.FinalPublicationReceipt{}, err
	}
	return projection.FinalPublicationReceipt{
		ProjectionRevisionID: publication.IDs.RevisionID,
		ProjectionRevision:   championEvent.ProjectionRevision,
		TournamentRevision:   completed.Revision,
		OutboxEventID:        championEvent.ID,
		OutboxOrdinal:        championEvent.ProjectionOrdinal,
		Changed:              true,
	}, nil
}

func (r *ProjectionPostgres) createFinalChampionOutboxEvent(
	ctx context.Context,
	publication projection.FinalPublication,
	projectionRevision int64,
) (sqlc.CreateFinalChampionOutboxEventRow, error) {
	championArtifactID, ok := finalChampionArtifactID(publication)
	if !ok || projectionRevision < 1 {
		return sqlc.CreateFinalChampionOutboxEventRow{}, domain.ErrConflict
	}
	payload, err := marshalJSON("ProjectionPostgres - PublishFinal - champion outbox payload", map[string]any{
		"schema":                 "tournament-champion-published-v1",
		"projection_revision_id": publication.IDs.RevisionID.String(),
		"projection_revision":    projectionRevision,
	})
	if err != nil {
		return sqlc.CreateFinalChampionOutboxEventRow{}, err
	}
	eventID := uuid.NewSHA1(publication.IDs.RevisionID, []byte("final-champion-outbox-event"))
	idempotencyKey := uuid.NewSHA1(publication.IDs.RevisionID, []byte("final-champion-outbox-idempotency"))
	event, err := r.tx.Querier(ctx).CreateFinalChampionOutboxEvent(ctx, sqlc.CreateFinalChampionOutboxEventParams{
		ID: eventID, TournamentID: publication.Scope.TournamentID, RosterID: publication.Scope.RosterID,
		ProjectionRevisionID: publication.IDs.RevisionID, ProjectionRevision: projectionRevision,
		Payload: payload, IdempotencyKey: idempotencyKey, CreatedAt: tstz(publication.PublishedAt),
		FinalSeriesID:         publication.Scope.SeriesID,
		FinalResultRevisionID: publication.Expected.SeriesResultRevisionID.UUID(),
		ChampionArtifactID:    championArtifactID,
	})
	if err != nil {
		return sqlc.CreateFinalChampionOutboxEventRow{}, projectionCASWriteError("create champion outbox event", err)
	}
	if !finalChampionOutboxMatches(event, publication, projectionRevision, eventID, payload) {
		return sqlc.CreateFinalChampionOutboxEventRow{}, fmt.Errorf("final champion outbox readback: %w", domain.ErrConflict)
	}
	return event, nil
}

func finalChampionOutboxMatches(event sqlc.CreateFinalChampionOutboxEventRow, publication projection.FinalPublication, revision int64, eventID uuid.UUID, payload []byte) bool {
	return event.ID == eventID && event.ProjectionRevisionID == publication.IDs.RevisionID &&
		event.TournamentID == publication.Scope.TournamentID && event.RosterID == publication.Scope.RosterID &&
		event.ProjectionRevision == revision && event.ProjectionOrdinal >= 1 &&
		event.Terminal && event.Audience == "all" && !event.PrincipalID.Valid &&
		event.Topic == "tournament.champion.published" && progressionJSONEqual(event.Payload, payload)
}

func finalChampionArtifactID(publication projection.FinalPublication) (uuid.UUID, bool) {
	for _, artifact := range publication.Artifacts {
		if artifact.Kind == domain.ArtifactKindChampion {
			return artifact.ID, artifact.ID != uuid.Nil
		}
	}
	return uuid.Nil, false
}

func finalProjectionRevisionConflict(
	publication projection.FinalPublication,
	aggregate sqlc.LockFinalProjectionAggregateRow,
	currentProjectionRevision int64,
	replay bool,
) error {
	expected := publication.Expected
	if replay {
		if aggregate.TournamentRevision == expected.TournamentRevision+1 &&
			domain.TournamentState(aggregate.TournamentState) == domain.TournamentStateCompleted &&
			currentProjectionRevision == expected.ProjectionRevision+1 {
			return nil
		}
	} else if aggregate.TournamentRevision == expected.TournamentRevision &&
		domain.TournamentState(aggregate.TournamentState) == domain.TournamentStatePlayoffs &&
		currentProjectionRevision == expected.ProjectionRevision {
		return nil
	}
	return &projection.FinalRevisionConflictError{
		TournamentID:               publication.Scope.TournamentID,
		ExpectedTournamentRevision: expected.TournamentRevision,
		CurrentTournamentRevision:  aggregate.TournamentRevision,
		CurrentTournamentState:     domain.TournamentState(aggregate.TournamentState),
		ExpectedProjectionRevision: expected.ProjectionRevision,
		CurrentProjectionRevision:  currentProjectionRevision,
	}
}

func validateFinalProjectionAuthority(
	publication projection.FinalPublication,
	aggregate sqlc.LockFinalProjectionAggregateRow,
	attempts []sqlc.LockFinalProjectionAttemptsRow,
	seriesHead sqlc.LockFinalProjectionSeriesResultHeadRow,
	scoreHead sqlc.LockFinalProjectionScoreHeadRow,
	commits []sqlc.LockFinalProjectionResultCommitsRow,
	terminalCommit sqlc.LockFinalProjectionResultCommitsRow,
) (err error) {
	defer func() {
		if errors.Is(err, domain.ErrConflict) {
			err = finalProjectionSourceConflict(publication, finalProjectionAuthority{
				aggregate: aggregate, attempts: attempts, seriesHead: seriesHead, scoreHead: scoreHead,
			})
		}
	}()
	expected := publication.Expected
	if !validFinalSeriesAuthority(expected, aggregate) {
		return domain.ErrConflict
	}
	firstWins, secondWins, err := validateFinalProjectionAttempts(
		publication,
		attempts,
		aggregate.FirstParticipantID,
		aggregate.SecondParticipantID,
	)
	if err != nil {
		return domain.ErrConflict
	}
	if !allFinalProjectionChecks(
		firstWins == aggregate.FirstParticipantWins,
		secondWins == aggregate.SecondParticipantWins,
		firstWins == 2 || secondWins == 2,
		firstWins != 2 || secondWins != 2,
		attempts[len(attempts)-1].ResultEventID == terminalCommit.ResultEventID,
		validFinalProjectionWinner(expected.WinnerID, aggregate, firstWins, secondWins),
	) {
		return domain.ErrConflict
	}
	if !validFinalProjectionHeads(
		expected,
		scoreHead,
		seriesHead,
		terminalCommit,
		firstWins,
		secondWins,
	) {
		return domain.ErrConflict
	}
	if !validFinalProjectionCommitAuthority(commits, attempts) {
		return domain.ErrConflict
	}
	if !bytes.Equal(terminalCommit.PayloadDigest, publication.ResultProjectionDigest[:]) {
		return domain.ErrConflict
	}
	if !validFinalProjectionArtifactKinds(terminalCommit.ArtifactKinds) {
		return domain.ErrConflict
	}
	return nil
}

func finalProjectionSourceConflict(publication projection.FinalPublication, authority finalProjectionAuthority) error {
	expected := publication.Expected
	revision, revisionID := finalProjectionCurrent(authority)
	conflict := &projection.FinalRevisionConflictError{
		TournamentID:                   publication.Scope.TournamentID,
		ExpectedTournamentRevision:     expected.TournamentRevision,
		CurrentTournamentRevision:      authority.aggregate.TournamentRevision,
		CurrentTournamentState:         domain.TournamentState(authority.aggregate.TournamentState),
		ExpectedProjectionRevision:     expected.ProjectionRevision,
		CurrentProjectionRevision:      revision,
		CurrentProjectionRevisionID:    revisionID,
		ExpectedGameAttemptRevision:    expected.GameAttemptRevision,
		ExpectedGameResultRevisionID:   expected.GameResultRevisionID,
		ExpectedSeriesRevision:         expected.SeriesRevision,
		CurrentSeriesRevision:          authority.aggregate.SeriesRevision,
		ExpectedScoreHeadRevision:      expected.ScoreHeadRevision,
		CurrentScoreHeadRevision:       authority.scoreHead.HeadRevision,
		ExpectedScoreRevisionID:        expected.ScoreRevisionID,
		CurrentScoreRevisionID:         domain.SeriesScoreRevisionID(authority.scoreHead.CurrentRevisionID),
		ExpectedSeriesResultRevisionID: expected.SeriesResultRevisionID,
		CurrentSeriesResultRevisionID:  domain.OfficialResultRevisionID(authority.seriesHead.CurrentRevisionID),
	}
	for _, attempt := range authority.attempts {
		if attempt.AttemptID == publication.Scope.GameAttemptID {
			conflict.CurrentGameAttemptRevision = attempt.AttemptRevision
			conflict.CurrentGameResultRevisionID = domain.OfficialResultRevisionID(attempt.HeadRevisionID)
			conflict.CurrentGameResultRevision = attempt.HeadRevision
			break
		}
	}
	return conflict
}

func validFinalSeriesAuthority(
	expected projection.FinalHeadExpectation,
	aggregate sqlc.LockFinalProjectionAggregateRow,
) bool {
	return allFinalProjectionChecks(
		aggregate.SeriesFormat == string(domain.SeriesFormatBO3),
		aggregate.SeriesState == string(domain.SeriesStateCompleted),
		aggregate.WinnerID.Valid,
		aggregate.WinnerID.UUID == expected.WinnerID,
		aggregate.SeriesRevision == expected.SeriesRevision,
		aggregate.CurrentScoreRevisionID.Valid,
		aggregate.CurrentScoreRevisionID.UUID == expected.ScoreRevisionID.UUID(),
		aggregate.CurrentResultRevisionID.Valid,
		aggregate.CurrentResultRevisionID.UUID == expected.SeriesResultRevisionID.UUID(),
	)
}

func validFinalProjectionWinner(
	winnerID uuid.UUID,
	aggregate sqlc.LockFinalProjectionAggregateRow,
	firstWins int16,
	secondWins int16,
) bool {
	switch {
	case firstWins == 2:
		return winnerID == aggregate.FirstParticipantID
	case secondWins == 2:
		return winnerID == aggregate.SecondParticipantID
	default:
		return false
	}
}

func validFinalProjectionHeads(
	expected projection.FinalHeadExpectation,
	scoreHead sqlc.LockFinalProjectionScoreHeadRow,
	seriesHead sqlc.LockFinalProjectionSeriesResultHeadRow,
	commit sqlc.LockFinalProjectionResultCommitsRow,
	firstWins int16,
	secondWins int16,
) bool {
	return allFinalProjectionChecks(
		commit.GameResultRevisionID == expected.GameResultRevisionID.UUID(),
		commit.SeriesScoreRevisionID == scoreHead.CurrentRevisionID,
		commit.SeriesResultRevisionID.Valid,
		commit.SeriesResultRevisionID.UUID == seriesHead.CurrentRevisionID,
		scoreHead.CurrentRevisionID == expected.ScoreRevisionID.UUID(),
		scoreHead.HeadRevision == expected.ScoreHeadRevision,
		scoreHead.RevisionNumber == scoreHead.HeadRevision,
		scoreHead.FirstParticipantWins == firstWins,
		scoreHead.SecondParticipantWins == secondWins,
		scoreHead.ResultEventID.Valid,
		scoreHead.ResultEventID.UUID == commit.ResultEventID,
		seriesHead.CurrentRevisionID == expected.SeriesResultRevisionID.UUID(),
		seriesHead.RevisionNumber == seriesHead.HeadRevision,
		seriesHead.ResultState == string(domain.SeriesStateCompleted),
		seriesHead.WinnerID.Valid,
		seriesHead.WinnerID.UUID == expected.WinnerID,
		validCompletedSeriesResultReason(seriesHead.ResultReason),
		seriesHead.ResultEventID == commit.ResultEventID,
	)
}

func validCompletedSeriesResultReason(reason string) bool {
	return reason == string(domain.SeriesResultReasonScoreComplete) ||
		reason == string(domain.SeriesResultReasonOperatorCorrection)
}

func validFinalProjectionCommitAuthority(
	commits []sqlc.LockFinalProjectionResultCommitsRow,
	attempts []sqlc.LockFinalProjectionAttemptsRow,
) bool {
	if !allFinalProjectionChecks(len(commits) >= 2, len(commits) <= 32767) {
		return false
	}
	byResultEvent := make(map[uuid.UUID]sqlc.LockFinalProjectionResultCommitsRow, len(commits))
	seenOutbox := make(map[uuid.UUID]struct{}, len(commits))
	seenEvidence := make(map[uuid.UUID]struct{}, len(commits))
	previousSequence := int64(0)
	for _, commit := range commits {
		if !validFinalProjectionCommitIdentity(commit, previousSequence) {
			return false
		}
		if _, exists := byResultEvent[commit.ResultEventID]; exists {
			return false
		}
		if _, exists := seenOutbox[commit.OutboxEventID]; exists {
			return false
		}
		if _, exists := seenEvidence[commit.ProjectionEvidenceID]; exists {
			return false
		}
		if !validFinalProjectionCommitBinding(commit) {
			return false
		}
		byResultEvent[commit.ResultEventID] = commit
		seenOutbox[commit.OutboxEventID] = struct{}{}
		seenEvidence[commit.ProjectionEvidenceID] = struct{}{}
		previousSequence = commit.OutboxSequence
	}
	for _, attempt := range attempts {
		commit, exists := byResultEvent[attempt.ResultEventID]
		if !exists || !allFinalProjectionChecks(
			commit.AttemptID == attempt.AttemptID,
			commit.GameResultRevisionID == attempt.HeadRevisionID,
		) {
			return false
		}
	}
	return true
}

func validFinalProjectionCommitIdentity(
	commit sqlc.LockFinalProjectionResultCommitsRow,
	previousSequence int64,
) bool {
	return allFinalProjectionChecks(
		commit.CommitID != uuid.Nil,
		commit.ResultEventID != uuid.Nil,
		commit.AttemptID != uuid.Nil,
		commit.GameResultRevisionID != uuid.Nil,
		commit.SeriesScoreRevisionID != uuid.Nil,
		commit.OutboxEventID != uuid.Nil,
		commit.ProjectionEvidenceID != uuid.Nil,
		commit.OutboxSequence > previousSequence,
	)
}

func validFinalProjectionCommitBinding(
	commit sqlc.LockFinalProjectionResultCommitsRow,
) bool {
	return allFinalProjectionChecks(
		commit.ProjectionRevisionID != uuid.Nil,
		commit.ProjectionOrdinal >= 1,
	)
}

func validateFinalProjectionAttempts(
	publication projection.FinalPublication,
	attempts []sqlc.LockFinalProjectionAttemptsRow,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
) (int16, int16, error) {
	if !allFinalProjectionChecks(
		len(attempts) >= 2,
		firstParticipantID != uuid.Nil,
		secondParticipantID != uuid.Nil,
		firstParticipantID != secondParticipantID,
	) {
		return 0, 0, domain.ErrConflict
	}
	expected := publication.Expected
	firstWins := int16(0)
	secondWins := int16(0)
	sequence := finalProjectionAttemptSequence{}
	for _, attempt := range attempts {
		if !validFinalProjectionAttemptRow(attempt) {
			return 0, 0, domain.ErrConflict
		}
		if !sequence.Accept(attempt) {
			return 0, 0, domain.ErrConflict
		}
		state := domain.GameState(attempt.ResultState)
		if !state.IsTerminal() {
			return 0, 0, domain.ErrConflict
		}
		if state == domain.GameStateCompleted {
			if !attempt.ResultWinnerID.Valid {
				return 0, 0, domain.ErrConflict
			}
			switch attempt.ResultWinnerID.UUID {
			case firstParticipantID:
				firstWins++
			case secondParticipantID:
				secondWins++
			default:
				return 0, 0, domain.ErrConflict
			}
		}
	}
	if !allFinalProjectionChecks(sequence.slot >= 2, sequence.slot <= 3) {
		return 0, 0, domain.ErrConflict
	}
	last := attempts[len(attempts)-1]
	if !allFinalProjectionChecks(
		last.AttemptID == publication.Scope.GameAttemptID,
		last.AttemptRevision == expected.GameAttemptRevision,
		last.HeadRevisionID == expected.GameResultRevisionID.UUID(),
		last.ResultState == string(domain.GameStateCompleted),
		last.ResultWinnerID.Valid,
		last.ResultWinnerID.UUID == expected.WinnerID,
		last.ResultEventID != uuid.Nil,
	) {
		return 0, 0, domain.ErrConflict
	}
	return firstWins, secondWins, nil
}

type finalProjectionAttemptSequence struct {
	slot            int16
	attempt         int32
	completedInSlot bool
}

func (s *finalProjectionAttemptSequence) Accept(attempt sqlc.LockFinalProjectionAttemptsRow) bool {
	if attempt.SlotNumber == s.slot {
		if !allFinalProjectionChecks(
			attempt.AttemptNumber == s.attempt+1,
			!s.completedInSlot,
		) {
			return false
		}
	} else {
		if !allFinalProjectionChecks(
			attempt.SlotNumber == s.slot+1,
			attempt.AttemptNumber == 1,
			s.slot == 0 || s.completedInSlot,
		) {
			return false
		}
		s.slot = attempt.SlotNumber
		s.completedInSlot = false
	}
	s.attempt = attempt.AttemptNumber
	s.completedInSlot = attempt.ResultState == string(domain.GameStateCompleted)
	return true
}

func validFinalProjectionAttemptRow(attempt sqlc.LockFinalProjectionAttemptsRow) bool {
	if !allFinalProjectionChecks(
		attempt.SlotNumber >= 1,
		attempt.SlotNumber <= 3,
		attempt.AttemptNumber >= 1,
		attempt.AttemptID != uuid.Nil,
		attempt.AttemptResultRevisionID.Valid,
		attempt.AttemptResultReason != nil,
	) {
		return false
	}
	if !allFinalProjectionChecks(
		attempt.AttemptResultRevisionID.UUID == attempt.HeadRevisionID,
		attempt.HeadRevision >= 1,
		attempt.ResultRevisionNumber == attempt.HeadRevision,
		attempt.AttemptState == attempt.ResultState,
		*attempt.AttemptResultReason == attempt.ResultReason,
		attempt.AttemptWinnerID.Valid == attempt.ResultWinnerID.Valid,
		domain.GameResultReason(attempt.ResultReason).IsLegalFor(domain.GameState(attempt.ResultState)),
		attempt.ResultWinnerID.Valid == (attempt.ResultState == string(domain.GameStateCompleted)),
	) {
		return false
	}
	if attempt.AttemptWinnerID.Valid {
		return attempt.AttemptWinnerID.UUID == attempt.ResultWinnerID.UUID
	}
	return true
}

func validFinalProjectionArtifactKinds(raw []byte) bool {
	var kinds []string
	if json.Unmarshal(raw, &kinds) != nil || len(kinds) != 4 {
		return false
	}
	wanted := map[string]bool{
		string(domain.ArtifactKindGameResult):   false,
		string(domain.ArtifactKindSeriesScore):  false,
		string(domain.ArtifactKindStandings):    false,
		string(domain.ArtifactKindSeriesResult): false,
	}
	for _, kind := range kinds {
		if _, exists := wanted[kind]; !exists || wanted[kind] {
			return false
		}
		wanted[kind] = true
	}
	return true
}

func finalProjectionPublishInput(publication projection.FinalPublication) ProjectionPublishInput {
	seriesResultRevisionID := publication.Expected.SeriesResultRevisionID.UUID()
	artifacts := make([]ProjectionArtifactInput, len(publication.Artifacts))
	for index, artifact := range publication.Artifacts {
		members := make([]ProjectionMemberInput, len(artifact.Members))
		for memberIndex, member := range artifact.Members {
			members[memberIndex] = ProjectionMemberInput{
				ParticipantID: member.ParticipantID,
				Position:      member.Position,
				ScoreMilli:    member.ScoreMilli,
			}
		}
		dependencies := make([]ProjectionDependencyInput, len(artifact.Dependencies))
		for dependencyIndex, dependency := range artifact.Dependencies {
			dependencies[dependencyIndex] = ProjectionDependencyInput{
				ID:                       dependency.ID,
				Kind:                     dependency.Kind,
				DependsOnArtifactID:      dependency.DependsOnArtifactID,
				OfficialResultRevisionID: dependency.OfficialResultRevisionID,
				OfficialResultSeriesID:   dependency.OfficialResultSeriesID,
				GoldenPositionCommitID:   dependency.GoldenPositionCommitID,
			}
		}
		artifacts[index] = ProjectionArtifactInput{
			ID:            artifact.ID,
			Kind:          artifact.Kind,
			Key:           artifact.Key,
			Payload:       append([]byte(nil), artifact.Payload...),
			PayloadDigest: artifact.PayloadDigest,
			Members:       members,
			Dependencies:  dependencies,
		}
	}
	return ProjectionPublishInput{
		IDs: ProjectionIDs{
			RevisionID: publication.IDs.RevisionID,
			CutoffID:   publication.IDs.CutoffID,
		},
		Scope: ProjectionScope{
			TournamentID: publication.Scope.TournamentID,
			RosterID:     publication.Scope.RosterID,
		},
		Source: ProjectionSource{
			Kind:                     projectionSourceOfficialResult,
			OfficialResultRevisionID: &seriesResultRevisionID,
			Reason:                   publication.Reason,
		},
		Artifacts:          artifacts,
		SupersessionReason: publication.SupersessionReason,
		CutoffAt:           publication.CutoffAt,
		CreatedAt:          publication.CreatedAt,
		PublishedAt:        publication.PublishedAt,
	}
}

func allFinalProjectionChecks(checks ...bool) bool {
	for _, check := range checks {
		if !check {
			return false
		}
	}
	return true
}
