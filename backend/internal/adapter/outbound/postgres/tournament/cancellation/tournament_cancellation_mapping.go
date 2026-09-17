package cancellation

import (
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
)

func cancellationTournamentRecord(row sqlc.Tournament) (*tournamentcancellation.CancellationTournamentRecord, error) {
	return mapCancellationTournament(
		row.ID,
		row.State,
		row.PausedFromState,
		row.Revision,
		row.UpdatedAt,
		row.FinishedAt,
	)
}

func foundCancellationRecord(
	row sqlc.FindTournamentCancellationRow,
) (*tournamentcancellation.TournamentCancellationRecord, error) {
	tournament, err := mapCancellationTournament(
		row.TournamentID,
		row.TournamentState,
		row.PausedFromState,
		row.TournamentRevision,
		row.TournamentUpdatedAt,
		row.TournamentFinishedAt,
	)
	if err != nil {
		return nil, err
	}
	return mapCancellationRecord(
		row.CommandID,
		row.TournamentID,
		row.RosterID,
		row.SourceRevision,
		row.ResultingRevision,
		row.SourceState,
		row.ActorID,
		row.Reason,
		row.AuditEventID,
		row.OutboxEventID,
		row.CancelledAt,
		row.CreatedAt,
		tournament,
	)
}

func mapCancellationRecord(
	commandID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	sourceRevision int64,
	resultingRevision int64,
	sourceState string,
	actorID uuid.UUID,
	reason string,
	auditEventID uuid.UUID,
	outboxEventID uuid.UUID,
	cancelledAt pgtype.Timestamptz,
	createdAt pgtype.Timestamptz,
	tournament *tournamentcancellation.CancellationTournamentRecord,
) (*tournamentcancellation.TournamentCancellationRecord, error) {
	if !validCancellationRow(
		commandID,
		tournamentID,
		rosterID,
		sourceRevision,
		resultingRevision,
		sourceState,
		actorID,
		reason,
		auditEventID,
		outboxEventID,
		cancelledAt,
		createdAt,
		tournament,
	) {
		return nil, domain.ErrInternal
	}
	cancelled := cancelledAt.Time.UTC()
	return &tournamentcancellation.TournamentCancellationRecord{
		Tournament: *tournament, CommandID: commandID, ActorID: actorID, Reason: reason,
		AuditEventID: auditEventID, OutboxEventID: outboxEventID, CancelledAt: cancelled,
	}, nil
}

func mapCancellationTournament(
	id uuid.UUID,
	stateValue string,
	pausedValue *string,
	revision int64,
	updatedAt pgtype.Timestamptz,
	finishedAt pgtype.Timestamptz,
) (*tournamentcancellation.CancellationTournamentRecord, error) {
	if id == uuid.Nil || revision < 1 || !updatedAt.Valid {
		return nil, domain.ErrInternal
	}
	state := domain.TournamentState(stateValue)
	pausedFromState := cancellationPausedFromState(pausedValue)
	if err := (domain.Tournament{State: state, PausedFromState: pausedFromState}).Validate(); err != nil {
		return nil, domain.ErrInternal
	}
	if state.IsTerminal() != finishedAt.Valid {
		return nil, domain.ErrInternal
	}
	updated := updatedAt.Time.UTC()
	if !domain.IsValidServerTime(updated) {
		return nil, domain.ErrInternal
	}
	record := &tournamentcancellation.CancellationTournamentRecord{
		ID: id, State: state, PausedFromState: pausedFromState, Revision: revision, UpdatedAt: updated,
	}
	if finishedAt.Valid {
		finished := finishedAt.Time.UTC()
		if !domain.IsValidServerTime(finished) {
			return nil, domain.ErrInternal
		}
		record.FinishedAt = &finished
	}
	return record, nil
}

func validCancellationRow(
	commandID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	sourceRevision int64,
	resultingRevision int64,
	sourceState string,
	actorID uuid.UUID,
	reason string,
	auditEventID uuid.UUID,
	outboxEventID uuid.UUID,
	cancelledAt pgtype.Timestamptz,
	createdAt pgtype.Timestamptz,
	tournament *tournamentcancellation.CancellationTournamentRecord,
) bool {
	return validCancellationIdentity(
		commandID,
		tournamentID,
		rosterID,
		actorID,
		reason,
		auditEventID,
		outboxEventID,
	) && validCancellationLineage(
		sourceRevision,
		resultingRevision,
		sourceState,
		tournamentID,
		tournament,
	) && validCancellationTimes(cancelledAt, createdAt, tournament)
}

func validCancellationIdentity(
	commandID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	actorID uuid.UUID,
	reason string,
	auditEventID uuid.UUID,
	outboxEventID uuid.UUID,
) bool {
	return commandID != uuid.Nil && tournamentID != uuid.Nil && rosterID != uuid.Nil && actorID != uuid.Nil &&
		auditEventID != uuid.Nil && outboxEventID != uuid.Nil && auditEventID != outboxEventID &&
		reason != "" && strings.TrimSpace(reason) == reason && len(reason) <= maxCancellationReasonLength
}

func validCancellationLineage(
	sourceRevision int64,
	resultingRevision int64,
	sourceState string,
	tournamentID uuid.UUID,
	tournament *tournamentcancellation.CancellationTournamentRecord,
) bool {
	source := domain.TournamentState(sourceState)
	return sourceRevision >= 1 && resultingRevision == sourceRevision+1 && source.IsValid() && !source.IsTerminal() &&
		tournament != nil && tournament.ID == tournamentID &&
		tournament.State == domain.TournamentStateCancelled && tournament.Revision == resultingRevision &&
		tournament.PausedFromState == nil && tournament.FinishedAt != nil
}

func validCancellationTimes(
	cancelledAt pgtype.Timestamptz,
	createdAt pgtype.Timestamptz,
	tournament *tournamentcancellation.CancellationTournamentRecord,
) bool {
	if !cancelledAt.Valid || !createdAt.Valid || tournament == nil || tournament.FinishedAt == nil {
		return false
	}
	cancelled := cancelledAt.Time.UTC()
	created := createdAt.Time.UTC()
	return domain.IsValidServerTime(cancelled) && domain.IsValidServerTime(created) && !created.Before(cancelled) &&
		tournament.FinishedAt.Equal(cancelled) &&
		tournament.UpdatedAt.Equal(cancelled)
}

func cancellationPausedFromState(value *string) *domain.TournamentState {
	if value == nil {
		return nil
	}
	state := domain.TournamentState(*value)
	return &state
}
