package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

const arenaOutboxProjectionMappingSQL = `
WITH committed_outbox AS (
    SELECT outbox_event.id AS outbox_event_id,
        outbox_event.tournament_id,
        result_commit.game_result_revision_id,
        result_commit.series_result_revision_id
    FROM arena_outbox_events AS outbox_event
    INNER JOIN arena_result_commits AS result_commit
        ON result_commit.outbox_event_id = outbox_event.id
        AND result_commit.result_event_id = outbox_event.result_event_id
        AND result_commit.tournament_id = outbox_event.tournament_id
        AND result_commit.roster_id = outbox_event.roster_id
        AND result_commit.series_id = outbox_event.series_id
),
commit_revisions AS (
    SELECT committed_outbox.outbox_event_id,
        committed_outbox.tournament_id,
        committed_outbox.game_result_revision_id AS official_result_revision_id
    FROM committed_outbox
    UNION ALL
    SELECT committed_outbox.outbox_event_id,
        committed_outbox.tournament_id,
        committed_outbox.series_result_revision_id AS official_result_revision_id
    FROM committed_outbox
    WHERE committed_outbox.series_result_revision_id IS NOT NULL
),
projection_matches AS (
    SELECT DISTINCT commit_revision.outbox_event_id,
        commit_revision.tournament_id,
        projection_revision.id AS projection_revision_id,
        projection_revision.revision_number
    FROM commit_revisions AS commit_revision
    INNER JOIN arena_projection_revisions AS projection_revision
        ON projection_revision.tournament_id = commit_revision.tournament_id
        AND projection_revision.state IN ('published', 'superseded')
    INNER JOIN arena_projection_cutoffs AS projection_cutoff
        ON projection_cutoff.id = projection_revision.cutoff_id
        AND projection_cutoff.tournament_id = projection_revision.tournament_id
        AND projection_cutoff.sequence_number = projection_revision.revision_number
    WHERE projection_cutoff.official_result_revision_id = commit_revision.official_result_revision_id
        OR EXISTS (
            SELECT 1
            FROM arena_projection_revision_artifacts AS revision_artifact
            INNER JOIN arena_projection_dependencies AS dependency
                ON dependency.artifact_id = revision_artifact.artifact_id
                AND dependency.tournament_id = revision_artifact.tournament_id
                AND dependency.roster_id = revision_artifact.roster_id
            WHERE revision_artifact.revision_id = projection_revision.id
                AND revision_artifact.tournament_id = projection_revision.tournament_id
                AND dependency.official_result_revision_id = commit_revision.official_result_revision_id
        )
),
ranked_matches AS (
    SELECT projection_match.outbox_event_id,
        projection_match.tournament_id,
        projection_match.projection_revision_id,
        projection_match.revision_number,
        ROW_NUMBER() OVER (
            PARTITION BY projection_match.outbox_event_id
            ORDER BY projection_match.revision_number,
                projection_match.projection_revision_id
        ) AS match_rank
    FROM projection_matches AS projection_match
),
first_matches AS (
    SELECT ranked_match.outbox_event_id,
        ranked_match.tournament_id,
        ranked_match.projection_revision_id,
        ranked_match.revision_number
    FROM ranked_matches AS ranked_match
    WHERE ranked_match.match_rank = 1
),
unambiguous_matches AS (
    SELECT first_match.outbox_event_id,
        first_match.tournament_id,
        first_match.projection_revision_id,
        first_match.revision_number
    FROM first_matches AS first_match
    INNER JOIN (
        SELECT tournament_id,
            revision_number
        FROM first_matches
        GROUP BY tournament_id, revision_number
        HAVING COUNT(DISTINCT outbox_event_id) = 1
    ) AS unique_revision
        ON unique_revision.tournament_id = first_match.tournament_id
        AND unique_revision.revision_number = first_match.revision_number
)
`

const arenaOutboxNextTournamentSQL = arenaOutboxProjectionMappingSQL + `
-- arena-outbox-next-tournament
SELECT outbox_event.tournament_id
FROM unambiguous_matches AS projection_match
INNER JOIN arena_outbox_events AS outbox_event
    ON outbox_event.id = projection_match.outbox_event_id
WHERE outbox_event.published_at IS NULL
ORDER BY projection_match.revision_number,
    outbox_event.created_at,
    outbox_event.id
LIMIT 1`

const arenaOutboxNextEventSQL = arenaOutboxProjectionMappingSQL + `
-- arena-outbox-next-event
SELECT outbox_event.id,
    outbox_event.tournament_id,
    outbox_event.result_event_id,
    outbox_event.topic,
    outbox_event.payload,
    outbox_event.created_at,
    projection_match.revision_number AS sequence,
    projection_match.revision_number AS projection_revision
FROM unambiguous_matches AS projection_match
INNER JOIN arena_outbox_events AS outbox_event
    ON outbox_event.id = projection_match.outbox_event_id
WHERE outbox_event.tournament_id = $1
    AND outbox_event.published_at IS NULL
ORDER BY projection_match.revision_number,
    outbox_event.created_at,
    outbox_event.id
LIMIT 1
FOR UPDATE OF outbox_event`

const arenaOutboxTournamentLockSQL = `
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`

const arenaOutboxMarkPublishedSQL = `
-- arena-outbox-mark-published
UPDATE arena_outbox_events
SET published_at = GREATEST(clock_timestamp(), created_at)
WHERE id = $1
    AND published_at IS NULL`

var (
	ErrArenaOutboxPublisherConfiguration = errors.New("arena outbox publisher: invalid configuration")
	ErrArenaOutboxPublicationConflict    = errors.New("arena outbox publisher: publication state changed")
	errArenaOutboxCandidateChanged       = errors.New("arena outbox publisher: candidate changed")
)

type ArenaOutboxEvent struct {
	ID                 uuid.UUID
	TournamentID       uuid.UUID
	ResultEventID      uuid.UUID
	Sequence           int64
	ProjectionRevision int64
	Topic              string
	Payload            json.RawMessage
	OccurredAt         time.Time
}

type ArenaOutboxSink interface {
	Publish(ctx context.Context, event ArenaOutboxEvent) error
}

type arenaOutboxTransaction interface {
	Do(ctx context.Context, fn func(context.Context) error) error
	Conn(ctx context.Context) sqlc.DBTX
}

type ArenaOutboxPublisher struct {
	tx   arenaOutboxTransaction
	sink ArenaOutboxSink
}

func NewArenaOutboxPublisher(tx *TxManager, sink ArenaOutboxSink) *ArenaOutboxPublisher {
	return newArenaOutboxPublisher(tx, sink)
}

func newArenaOutboxPublisher(tx arenaOutboxTransaction, sink ArenaOutboxSink) *ArenaOutboxPublisher {
	return &ArenaOutboxPublisher{tx: tx, sink: sink}
}

//nolint:gocyclo // One transaction keeps candidate retry, sink delivery, and the publication marker ordered.
func (publisher *ArenaOutboxPublisher) PublishNext(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if publisher == nil || publisher.tx == nil || publisher.sink == nil {
		return false, ErrArenaOutboxPublisherConfiguration
	}

	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		published := false
		err := publisher.tx.Do(ctx, func(txCtx context.Context) error {
			conn := publisher.tx.Conn(txCtx)
			tournamentID, found, err := nextArenaOutboxTournament(txCtx, conn)
			if err != nil || !found {
				return err
			}
			if _, err := conn.Exec(txCtx, arenaOutboxTournamentLockSQL, tournamentID); err != nil {
				return fmt.Errorf("ArenaOutboxPublisher - lock tournament: %w", err)
			}

			event, found, err := nextArenaOutboxEvent(txCtx, conn, tournamentID)
			if err != nil {
				return err
			}
			if !found {
				return errArenaOutboxCandidateChanged
			}
			if err := publisher.sink.Publish(txCtx, cloneArenaOutboxEvent(event)); err != nil {
				return fmt.Errorf("ArenaOutboxPublisher - publish: %w", err)
			}
			commandTag, err := conn.Exec(txCtx, arenaOutboxMarkPublishedSQL, event.ID)
			if err != nil {
				return fmt.Errorf("ArenaOutboxPublisher - mark published: %w", err)
			}
			if commandTag.RowsAffected() != 1 {
				return ErrArenaOutboxPublicationConflict
			}
			published = true
			return nil
		})
		if errors.Is(err, errArenaOutboxCandidateChanged) {
			continue
		}
		if err != nil {
			return false, err
		}
		return published, nil
	}
}

func nextArenaOutboxTournament(
	ctx context.Context,
	conn sqlc.DBTX,
) (uuid.UUID, bool, error) {
	rows, err := conn.Query(ctx, arenaOutboxNextTournamentSQL)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("ArenaOutboxPublisher - select tournament: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		rows.Close()
		if err := rows.Err(); err != nil {
			return uuid.Nil, false, fmt.Errorf("ArenaOutboxPublisher - select tournament rows: %w", err)
		}
		return uuid.Nil, false, nil
	}
	var tournamentID uuid.UUID
	if err := rows.Scan(&tournamentID); err != nil {
		return uuid.Nil, false, fmt.Errorf("ArenaOutboxPublisher - scan tournament: %w", err)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return uuid.Nil, false, fmt.Errorf("ArenaOutboxPublisher - select tournament rows: %w", err)
	}
	if tournamentID == uuid.Nil {
		return uuid.Nil, false, ErrArenaOutboxPublicationConflict
	}
	return tournamentID, true, nil
}

func nextArenaOutboxEvent(
	ctx context.Context,
	conn sqlc.DBTX,
	tournamentID uuid.UUID,
) (ArenaOutboxEvent, bool, error) {
	rows, err := conn.Query(ctx, arenaOutboxNextEventSQL, tournamentID)
	if err != nil {
		return ArenaOutboxEvent{}, false, fmt.Errorf("ArenaOutboxPublisher - select event: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		rows.Close()
		if err := rows.Err(); err != nil {
			return ArenaOutboxEvent{}, false, fmt.Errorf("ArenaOutboxPublisher - select event rows: %w", err)
		}
		return ArenaOutboxEvent{}, false, nil
	}
	var event ArenaOutboxEvent
	var payload []byte
	if err := rows.Scan(
		&event.ID,
		&event.TournamentID,
		&event.ResultEventID,
		&event.Topic,
		&payload,
		&event.OccurredAt,
		&event.Sequence,
		&event.ProjectionRevision,
	); err != nil {
		return ArenaOutboxEvent{}, false, fmt.Errorf("ArenaOutboxPublisher - scan event: %w", err)
	}
	event.Payload = append(json.RawMessage(nil), payload...)
	rows.Close()
	if err := rows.Err(); err != nil {
		return ArenaOutboxEvent{}, false, fmt.Errorf("ArenaOutboxPublisher - select event rows: %w", err)
	}
	if !validArenaOutboxEvent(event, tournamentID) {
		return ArenaOutboxEvent{}, false, ErrArenaOutboxPublicationConflict
	}
	event.OccurredAt = event.OccurredAt.UTC()
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	return event, true, nil
}

func validArenaOutboxEvent(event ArenaOutboxEvent, tournamentID uuid.UUID) bool {
	return event.ID != uuid.Nil && event.TournamentID == tournamentID && event.ResultEventID != uuid.Nil &&
		event.Sequence >= 1 && event.ProjectionRevision >= 1 && event.Sequence == event.ProjectionRevision &&
		strings.TrimSpace(event.Topic) != "" && json.Valid(event.Payload) && !event.OccurredAt.IsZero()
}

func cloneArenaOutboxEvent(event ArenaOutboxEvent) ArenaOutboxEvent {
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	return event
}

var _ arenaOutboxTransaction = (*TxManager)(nil)
