package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestArenaOutboxPublisher(t *testing.T) {
	t.Parallel()

	t.Run("publishes committed events in durable tournament projection order", func(t *testing.T) {
		t.Parallel()
		tournamentID := outboxTestUUID(1)
		db := &outboxTestDB{events: []*outboxTestRecord{
			outboxTestEvent(11, tournamentID, 1, "published", false, false),
			outboxTestEvent(12, tournamentID, 3, "published", true, false),
			outboxTestEvent(13, tournamentID, 2, "superseded", true, false),
			outboxTestEvent(14, tournamentID, 4, "draft", true, false),
			outboxTestEvent(15, tournamentID, 5, "published", true, true),
		}}
		sink := &outboxTestSink{}
		publisher := newArenaOutboxPublisher(&outboxTestTx{db: db}, sink)

		for range 2 {
			published, err := publisher.PublishNext(context.Background())
			if err != nil {
				t.Fatalf("PublishNext() error = %v", err)
			}
			if !published {
				t.Fatal("PublishNext() published = false, want true")
			}
		}
		published, err := publisher.PublishNext(context.Background())
		if err != nil {
			t.Fatalf("empty PublishNext() error = %v", err)
		}
		if published {
			t.Fatal("empty PublishNext() published = true, want false")
		}

		got := sink.Events()
		if len(got) != 2 {
			t.Fatalf("published events = %d, want 2", len(got))
		}
		if got[0].Sequence != 2 || got[1].Sequence != 3 {
			t.Fatalf("published sequences = [%d %d], want [2 3]", got[0].Sequence, got[1].Sequence)
		}
		if got[0].ID != outboxTestUUID(13) || got[1].ID != outboxTestUUID(12) {
			t.Fatalf("published IDs = [%s %s], want official projection order", got[0].ID, got[1].ID)
		}
		if got[0].ProjectionRevision != 2 || got[1].ProjectionRevision != 3 {
			t.Fatalf("projection revisions = [%d %d], want [2 3]", got[0].ProjectionRevision, got[1].ProjectionRevision)
		}

		queries := strings.Join(db.Queries(), "\n")
		for _, fragment := range []string{
			"arena_result_commits", "arena_projection_cutoffs", "arena_projection_revisions",
			"arena_projection_revision_artifacts", "arena_projection_dependencies",
			"series_result_revision_id", "state IN ('published', 'superseded')",
			"COUNT(DISTINCT", "ORDER BY", "revision_number",
		} {
			if !strings.Contains(queries, fragment) {
				t.Fatalf("publisher SQL does not prove %q contract:\n%s", fragment, queries)
			}
		}
	})

	t.Run("sink failure leaves the row unpublished for retry", func(t *testing.T) {
		t.Parallel()
		tournamentID := outboxTestUUID(2)
		record := outboxTestEvent(21, tournamentID, 7, "published", true, false)
		db := &outboxTestDB{events: []*outboxTestRecord{record}}
		sink := &outboxTestSink{failures: 1}
		publisher := newArenaOutboxPublisher(&outboxTestTx{db: db}, sink)

		if published, err := publisher.PublishNext(context.Background()); err == nil || published {
			t.Fatalf("failed PublishNext() = (%v, %v), want (false, error)", published, err)
		}
		if record.Published() {
			t.Fatal("sink failure marked the outbox row published")
		}
		published, err := publisher.PublishNext(context.Background())
		if err != nil || !published {
			t.Fatalf("retry PublishNext() = (%v, %v), want (true, nil)", published, err)
		}
		requireStableOutboxRetry(t, sink.Events())
	})

	t.Run("mark failure permits an at least once duplicate", func(t *testing.T) {
		t.Parallel()
		tournamentID := outboxTestUUID(3)
		record := outboxTestEvent(31, tournamentID, 9, "published", true, false)
		db := &outboxTestDB{events: []*outboxTestRecord{record}, markFailures: 1}
		sink := &outboxTestSink{}
		publisher := newArenaOutboxPublisher(&outboxTestTx{db: db}, sink)

		if published, err := publisher.PublishNext(context.Background()); err == nil || published {
			t.Fatalf("mark failure PublishNext() = (%v, %v), want (false, error)", published, err)
		}
		if record.Published() {
			t.Fatal("mark failure left the outbox row published")
		}
		published, err := publisher.PublishNext(context.Background())
		if err != nil || !published {
			t.Fatalf("retry PublishNext() = (%v, %v), want (true, nil)", published, err)
		}
		requireStableOutboxRetry(t, sink.Events())
	})

	t.Run("commit failure retries the same stable identity and sequence", func(t *testing.T) {
		t.Parallel()
		tournamentID := outboxTestUUID(4)
		record := outboxTestEvent(41, tournamentID, 12, "published", true, false)
		db := &outboxTestDB{events: []*outboxTestRecord{record}}
		sink := &outboxTestSink{}
		publisher := newArenaOutboxPublisher(&outboxTestTx{db: db, commitFailures: 1}, sink)

		if published, err := publisher.PublishNext(context.Background()); err == nil || published {
			t.Fatalf("commit failure PublishNext() = (%v, %v), want (false, error)", published, err)
		}
		if record.Published() {
			t.Fatal("commit failure did not roll back published_at")
		}
		published, err := publisher.PublishNext(context.Background())
		if err != nil || !published {
			t.Fatalf("retry PublishNext() = (%v, %v), want (true, nil)", published, err)
		}
		requireStableOutboxRetry(t, sink.Events())
	})

	t.Run("cancellation stops before a query or sink call", func(t *testing.T) {
		t.Parallel()
		db := &outboxTestDB{events: []*outboxTestRecord{
			outboxTestEvent(51, outboxTestUUID(5), 1, "published", true, false),
		}}
		sink := &outboxTestSink{}
		publisher := newArenaOutboxPublisher(&outboxTestTx{db: db}, sink)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		published, err := publisher.PublishNext(ctx)
		if !errors.Is(err, context.Canceled) || published {
			t.Fatalf("cancelled PublishNext() = (%v, %v), want (false, context.Canceled)", published, err)
		}
		if len(db.Queries()) != 0 || len(sink.Events()) != 0 {
			t.Fatal("cancelled publisher reached storage or sink")
		}
	})

	t.Run("candidate change after tournament lock is retried", func(t *testing.T) {
		t.Parallel()
		tournamentID := outboxTestUUID(7)
		db := &outboxTestDB{
			events:               []*outboxTestRecord{outboxTestEvent(71, tournamentID, 4, "published", true, false)},
			emptyEventSelections: 1,
		}
		sink := &outboxTestSink{}
		publisher := newArenaOutboxPublisher(&outboxTestTx{db: db}, sink)

		published, err := publisher.PublishNext(context.Background())
		if err != nil || !published {
			t.Fatalf("PublishNext() after candidate change = (%v, %v), want (true, nil)", published, err)
		}
		if len(sink.Events()) != 1 {
			t.Fatalf("sink calls = %d, want 1", len(sink.Events()))
		}
	})

	t.Run("row iteration errors are returned after rows close", func(t *testing.T) {
		t.Parallel()
		db := &outboxTestDB{
			events:   []*outboxTestRecord{outboxTestEvent(61, outboxTestUUID(6), 1, "published", true, false)},
			rowError: errors.New("read interrupted"),
		}
		publisher := newArenaOutboxPublisher(&outboxTestTx{db: db}, &outboxTestSink{})

		published, err := publisher.PublishNext(context.Background())
		if err == nil || published {
			t.Fatalf("row error PublishNext() = (%v, %v), want (false, error)", published, err)
		}
		if !db.AllRowsClosed() {
			t.Fatal("publisher did not close query rows")
		}
	})
}

func requireStableOutboxRetry(t *testing.T, events []ArenaOutboxEvent) {
	t.Helper()
	if len(events) != 2 {
		t.Fatalf("sink calls = %d, want 2", len(events))
	}
	if events[0].ID != events[1].ID || events[0].ResultEventID != events[1].ResultEventID {
		t.Fatalf("retry identity changed: first=%s/%s second=%s/%s", events[0].ID, events[0].ResultEventID, events[1].ID, events[1].ResultEventID)
	}
	if events[0].Sequence != events[1].Sequence || events[0].ProjectionRevision != events[1].ProjectionRevision {
		t.Fatalf("retry cursor changed: first=%d/%d second=%d/%d", events[0].Sequence, events[0].ProjectionRevision, events[1].Sequence, events[1].ProjectionRevision)
	}
}

type outboxTestRecord struct {
	mu                 sync.Mutex
	id                 uuid.UUID
	tournamentID       uuid.UUID
	resultEventID      uuid.UUID
	topic              string
	payload            []byte
	createdAt          time.Time
	sequence           int64
	projectionRevision int64
	projectionState    string
	committed          bool
	ambiguous          bool
	published          bool
}

func outboxTestEvent(number int, tournamentID uuid.UUID, sequence int64, state string, committed, ambiguous bool) *outboxTestRecord {
	return &outboxTestRecord{
		id:                 outboxTestUUID(number),
		tournamentID:       tournamentID,
		resultEventID:      outboxTestUUID(number + 1000),
		topic:              "arena.result.committed",
		payload:            []byte(fmt.Sprintf(`{"result_event_id":%q}`, outboxTestUUID(number+1000))),
		createdAt:          time.Date(2026, 9, 2, 8, 0, number, 0, time.UTC),
		sequence:           sequence,
		projectionRevision: sequence,
		projectionState:    state,
		committed:          committed,
		ambiguous:          ambiguous,
	}
}

func (r *outboxTestRecord) Published() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.published
}

type outboxTestSink struct {
	mu       sync.Mutex
	events   []ArenaOutboxEvent
	failures int
}

func (s *outboxTestSink) Publish(ctx context.Context, event ArenaOutboxEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	event.Payload = append([]byte(nil), event.Payload...)
	s.events = append(s.events, event)
	if s.failures > 0 {
		s.failures--
		return errors.New("sink unavailable")
	}
	return nil
}

func (s *outboxTestSink) Events() []ArenaOutboxEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := append([]ArenaOutboxEvent(nil), s.events...)
	for index := range result {
		result[index].Payload = append([]byte(nil), result[index].Payload...)
	}
	return result
}

type outboxTestTx struct {
	db             *outboxTestDB
	mu             sync.Mutex
	commitFailures int
}

func (tx *outboxTestTx) Do(ctx context.Context, fn func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	snapshot := tx.db.publishedSnapshot()
	if err := fn(ctx); err != nil {
		tx.db.restorePublished(snapshot)
		return err
	}
	if tx.commitFailures > 0 {
		tx.commitFailures--
		tx.db.restorePublished(snapshot)
		return errors.New("commit interrupted")
	}
	return nil
}

func (tx *outboxTestTx) Conn(context.Context) sqlc.DBTX {
	return tx.db
}

type outboxTestDB struct {
	mu                   sync.Mutex
	events               []*outboxTestRecord
	queries              []string
	rows                 []*outboxTestRows
	markFailures         int
	rowError             error
	emptyEventSelections int
}

func (db *outboxTestDB) Query(ctx context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.queries = append(db.queries, query)

	var values [][]any
	switch {
	case strings.Contains(query, "arena-outbox-next-tournament"):
		if record := db.nextReady(uuid.Nil); record != nil {
			values = [][]any{{record.tournamentID}}
		}
	case strings.Contains(query, "arena-outbox-next-event"):
		if len(args) != 1 {
			return nil, fmt.Errorf("next event args = %d, want 1", len(args))
		}
		tournamentID, ok := args[0].(uuid.UUID)
		if !ok {
			return nil, fmt.Errorf("next event tournament argument = %T", args[0])
		}
		if db.emptyEventSelections > 0 {
			db.emptyEventSelections--
		} else if record := db.nextReady(tournamentID); record != nil {
			values = [][]any{{
				record.id, record.tournamentID, record.resultEventID, record.topic,
				append([]byte(nil), record.payload...), record.createdAt,
				record.sequence, record.projectionRevision,
			}}
		}
	default:
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
	rows := &outboxTestRows{values: values, terminalErr: db.rowError}
	db.rowError = nil
	db.rows = append(db.rows, rows)
	return rows, nil
}

func (db *outboxTestDB) Exec(ctx context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	if err := ctx.Err(); err != nil {
		return pgconn.CommandTag{}, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.queries = append(db.queries, query)
	if strings.Contains(query, "pg_advisory_xact_lock") {
		return pgconn.NewCommandTag("SELECT 1"), nil
	}
	if !strings.Contains(query, "arena-outbox-mark-published") {
		return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", query)
	}
	if db.markFailures > 0 {
		db.markFailures--
		return pgconn.CommandTag{}, errors.New("mark interrupted")
	}
	if len(args) != 1 {
		return pgconn.CommandTag{}, fmt.Errorf("mark args = %d, want 1", len(args))
	}
	id, ok := args[0].(uuid.UUID)
	if !ok {
		return pgconn.CommandTag{}, fmt.Errorf("mark ID argument = %T", args[0])
	}
	for _, record := range db.events {
		if record.id != id || record.published {
			continue
		}
		record.mu.Lock()
		record.published = true
		record.mu.Unlock()
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

func (db *outboxTestDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return outboxTestRow{err: errors.New("unexpected QueryRow")}
}

func (db *outboxTestDB) nextReady(tournamentID uuid.UUID) *outboxTestRecord {
	var selected *outboxTestRecord
	for _, record := range db.events {
		if tournamentID != uuid.Nil && record.tournamentID != tournamentID {
			continue
		}
		if !record.committed || record.ambiguous || record.published ||
			(record.projectionState != "published" && record.projectionState != "superseded") {
			continue
		}
		if selected == nil || record.sequence < selected.sequence ||
			(record.sequence == selected.sequence && strings.Compare(record.id.String(), selected.id.String()) < 0) {
			selected = record
		}
	}
	return selected
}

func (db *outboxTestDB) publishedSnapshot() map[uuid.UUID]bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	result := make(map[uuid.UUID]bool, len(db.events))
	for _, record := range db.events {
		record.mu.Lock()
		result[record.id] = record.published
		record.mu.Unlock()
	}
	return result
}

func (db *outboxTestDB) restorePublished(snapshot map[uuid.UUID]bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, record := range db.events {
		record.mu.Lock()
		record.published = snapshot[record.id]
		record.mu.Unlock()
	}
}

func (db *outboxTestDB) Queries() []string {
	db.mu.Lock()
	defer db.mu.Unlock()
	return append([]string(nil), db.queries...)
}

func (db *outboxTestDB) AllRowsClosed() bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, rows := range db.rows {
		if !rows.Closed() {
			return false
		}
	}
	return true
}

type outboxTestRows struct {
	mu          sync.Mutex
	values      [][]any
	index       int
	current     []any
	closed      bool
	terminalErr error
}

func (rows *outboxTestRows) Close() {
	rows.mu.Lock()
	rows.closed = true
	rows.mu.Unlock()
}

func (rows *outboxTestRows) Err() error {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	if rows.closed {
		return rows.terminalErr
	}
	return nil
}

func (rows *outboxTestRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT 1") }
func (rows *outboxTestRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (rows *outboxTestRows) Next() bool {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	if rows.closed || rows.index >= len(rows.values) {
		rows.closed = true
		return false
	}
	rows.current = rows.values[rows.index]
	rows.index++
	return true
}

func (rows *outboxTestRows) Scan(dest ...any) error {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	if rows.current == nil || len(dest) != len(rows.current) {
		return fmt.Errorf("scan destinations = %d, values = %d", len(dest), len(rows.current))
	}
	for index := range dest {
		if err := assignOutboxTestValue(dest[index], rows.current[index]); err != nil {
			return fmt.Errorf("scan column %d: %w", index, err)
		}
	}
	return nil
}

func (rows *outboxTestRows) Values() ([]any, error) {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	return append([]any(nil), rows.current...), nil
}

func (rows *outboxTestRows) RawValues() [][]byte { return nil }
func (rows *outboxTestRows) Conn() *pgx.Conn     { return nil }
func (rows *outboxTestRows) Closed() bool {
	rows.mu.Lock()
	defer rows.mu.Unlock()
	return rows.closed
}

func assignOutboxTestValue(destination, value any) error {
	switch target := destination.(type) {
	case *uuid.UUID:
		typed, ok := value.(uuid.UUID)
		if !ok {
			return fmt.Errorf("cannot assign %T to UUID", value)
		}
		*target = typed
	case *string:
		typed, ok := value.(string)
		if !ok {
			return fmt.Errorf("cannot assign %T to string", value)
		}
		*target = typed
	case *[]byte:
		typed, ok := value.([]byte)
		if !ok {
			return fmt.Errorf("cannot assign %T to bytes", value)
		}
		*target = append([]byte(nil), typed...)
	case *time.Time:
		typed, ok := value.(time.Time)
		if !ok {
			return fmt.Errorf("cannot assign %T to time", value)
		}
		*target = typed
	case *int64:
		typed, ok := value.(int64)
		if !ok {
			return fmt.Errorf("cannot assign %T to int64", value)
		}
		*target = typed
	default:
		return fmt.Errorf("unsupported destination %T", destination)
	}
	return nil
}

type outboxTestRow struct{ err error }

func (row outboxTestRow) Scan(...any) error { return row.err }

func outboxTestUUID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", number))
}
