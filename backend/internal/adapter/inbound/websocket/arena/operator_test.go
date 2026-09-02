package arena

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestArenaOperatorRealtime(t *testing.T) {
	t.Parallel()

	tournamentID := testUUID("b0000000-0000-4000-8000-000000000001")
	otherTournamentID := testUUID("b0000000-0000-4000-8000-000000000002")
	principal := OperatorRealtimePrincipal{
		Authenticated: true,
		PrincipalID:   testUUID("b0000000-0000-4000-8000-000000000003"),
		Role:          OperatorRealtimeRole,
		TournamentID:  tournamentID,
	}
	baseState := operatorRealtimeState(t, tournamentID, 11, 16)

	t.Run("requires authenticated operator in exact tournament scope", func(t *testing.T) {
		tests := []struct {
			name       string
			principal  OperatorRealtimePrincipal
			tournament uuid.UUID
			want       error
		}{
			{name: "missing authentication", principal: OperatorRealtimePrincipal{}, tournament: tournamentID, want: ErrOperatorRealtimeAuthentication},
			{name: "wrong role", principal: OperatorRealtimePrincipal{Authenticated: true, PrincipalID: principal.PrincipalID, Role: "participant", TournamentID: tournamentID}, tournament: tournamentID, want: ErrOperatorRealtimeRole},
			{name: "wrong tournament", principal: principal, tournament: otherTournamentID, want: ErrOperatorRealtimeScope},
		}
		for _, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				source := &operatorRealtimeSourceFake{state: baseState}
				_, err := NewOperatorRealtimeAdapter(source).Read(context.Background(), testCase.principal, testCase.tournament, nil)
				if !errors.Is(err, testCase.want) {
					t.Fatalf("Read() error = %v, want %v", err, testCase.want)
				}
				if source.reads != 0 {
					t.Fatalf("unauthorized Read() called source %d times", source.reads)
				}
				operatorRealtimeRequireSanitizedError(t, err, "participant", otherTournamentID.String(), "cursor=raw")
			})
		}
	})

	t.Run("returns authoritative initial snapshot with operator evidence only", func(t *testing.T) {
		source := &operatorRealtimeSourceFake{state: baseState}
		result, err := NewOperatorRealtimeAdapter(source).Read(context.Background(), principal, tournamentID, nil)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if !result.UsesSnapshot || len(result.Envelopes) != 1 || result.Envelopes[0].Operator == nil {
			t.Fatalf("Read() = snapshot %t, envelopes %d", result.UsesSnapshot, len(result.Envelopes))
		}
		if source.reads != 1 || source.query.ReplayLimit != OperatorRealtimeReplayLimit || source.query.TournamentID != tournamentID || source.query.Cursor != nil {
			t.Fatalf("source query = %#v, reads = %d", source.query, source.reads)
		}
		if source.mutations != 0 {
			t.Fatalf("read adapter used mutation surface %d times", source.mutations)
		}

		body, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		for _, field := range []string{"waves", "members", "readiness_revision", "presence", "replays", "pause", "correction_revision", "audit_links", "audit_event_id", "official_result_revision_id"} {
			if !strings.Contains(string(body), `"`+field+`"`) {
				t.Fatalf("operator JSON missing %q: %s", field, body)
			}
		}
		lower := strings.ToLower(string(body))
		for _, forbidden := range []string{"flag_sentinel", "credential_sentinel", "private_task_sentinel", "command_sentinel", `"flag"`, `"credential"`, `"client_command"`} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("operator JSON contains forbidden %q: %s", forbidden, body)
			}
		}
		if strings.Contains(string(body), `"participant":`) || strings.Contains(string(body), `"public":`) {
			t.Fatalf("operator JSON contains another role payload: %s", body)
		}

		result.Envelopes[0].Operator.Waves[0].Members[0].Ready = false
		result.Envelopes[0].Operator.Pause.Reason = "changed result"
		if !source.state.Snapshot.Operator.Waves[0].Members[0].Ready || source.state.Snapshot.Operator.Pause.Reason == "changed result" {
			t.Fatal("returned snapshot aliases source state")
		}
	})

	t.Run("resumes ordered operator evidence and copies cursor and events", func(t *testing.T) {
		cursor := &RealtimeCursor{SchemaVersion: ArenaRealtimeSchemaVersion, TournamentID: tournamentID, LastSequence: 11, ProjectionRevision: 11}
		source := &operatorRealtimeSourceFake{state: baseState, mutateQueryCursor: true}
		result, err := NewOperatorRealtimeAdapter(source).Read(context.Background(), principal, tournamentID, cursor)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if result.UsesSnapshot || len(result.Envelopes) != 5 {
			t.Fatalf("Read() = snapshot %t, envelopes %d", result.UsesSnapshot, len(result.Envelopes))
		}
		for index, envelope := range result.Envelopes {
			wantSequence := int64(index + 12)
			if envelope.Sequence != wantSequence || envelope.Operator == nil || envelope.Participant != nil || envelope.Public != nil {
				t.Fatalf("envelope[%d] = sequence %d, roles operator=%t participant=%t public=%t", index, envelope.Sequence, envelope.Operator != nil, envelope.Participant != nil, envelope.Public != nil)
			}
		}
		if cursor.LastSequence != 11 || cursor.ProjectionRevision != 11 {
			t.Fatalf("Read() mutated caller cursor: %#v", cursor)
		}
		result.Envelopes[0].Operator.Presence[0].State = "changed result"
		if source.state.Events[1].Operator.Presence[0].State == "changed result" {
			t.Fatal("returned replay aliases source events")
		}
	})

	for _, testCase := range []struct {
		name   string
		cursor RealtimeCursor
	}{
		{name: "stale cursor", cursor: RealtimeCursor{SchemaVersion: ArenaRealtimeSchemaVersion, TournamentID: tournamentID, LastSequence: 10, ProjectionRevision: 10}},
		{name: "future cursor", cursor: RealtimeCursor{SchemaVersion: ArenaRealtimeSchemaVersion, TournamentID: tournamentID, LastSequence: 18, ProjectionRevision: 18}},
	} {
		t.Run(testCase.name+" returns exactly one snapshot", func(t *testing.T) {
			result, err := NewOperatorRealtimeAdapter(&operatorRealtimeSourceFake{state: baseState}).Read(context.Background(), principal, tournamentID, &testCase.cursor)
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}
			if !result.UsesSnapshot || len(result.Envelopes) != 1 || result.Envelopes[0].Operator == nil || result.Envelopes[0].Sequence != baseState.Snapshot.Sequence {
				t.Fatalf("Read() = snapshot %t, envelopes %#v", result.UsesSnapshot, result.Envelopes)
			}
		})
	}

	t.Run("rejects malformed cursor with a sanitized cursor error", func(t *testing.T) {
		cursor := &RealtimeCursor{SchemaVersion: ArenaRealtimeSchemaVersion, TournamentID: tournamentID}
		source := &operatorRealtimeSourceFake{state: baseState}
		_, err := NewOperatorRealtimeAdapter(source).Read(context.Background(), principal, tournamentID, cursor)
		if !errors.Is(err, ErrOperatorRealtimeCursor) || source.reads != 0 {
			t.Fatalf("Read() error = %v, reads = %d", err, source.reads)
		}
		operatorRealtimeRequireSanitizedError(t, err, tournamentID.String(), "last_sequence", "projection_revision")
	})

	t.Run("rejects participant and public event payloads", func(t *testing.T) {
		publicSnapshot := testPublicSnapshot(t, tournamentID)
		publicEnvelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
			SchemaVersion: ArenaRealtimeSchemaVersion, TournamentID: tournamentID,
			Sequence: publicSnapshot.LastSequence, ProjectionRevision: publicSnapshot.Revision,
			EventID: testUUID("b0000000-0000-4000-8000-000000000090"), OccurredAt: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
		}, publicSnapshot)
		if err != nil {
			t.Fatal(err)
		}
		foreign := []RealtimeEnvelope{
			resumeEnvelope(t, tournamentID, testUUID("b0000000-0000-4000-8000-000000000091"), 12, 12),
			publicEnvelope,
		}
		for index, envelope := range foreign {
			t.Run(fmt.Sprintf("role-%d", index), func(t *testing.T) {
				candidate := baseState
				candidate.Events = []RealtimeEnvelope{envelope}
				_, err := NewOperatorRealtimeAdapter(&operatorRealtimeSourceFake{state: candidate}).Read(context.Background(), principal, tournamentID, nil)
				if !errors.Is(err, ErrOperatorRealtimePayload) {
					t.Fatalf("Read() error = %v", err)
				}
			})
		}
	})

	t.Run("bounds replay input", func(t *testing.T) {
		state := operatorRealtimeState(t, tournamentID, 1, OperatorRealtimeReplayLimit+1)
		_, err := NewOperatorRealtimeAdapter(&operatorRealtimeSourceFake{state: state}).Read(context.Background(), principal, tournamentID, nil)
		if !errors.Is(err, ErrOperatorRealtimeReplayLimit) {
			t.Fatalf("Read() error = %v", err)
		}
	})

	t.Run("sanitizes source failures", func(t *testing.T) {
		source := &operatorRealtimeSourceFake{err: errors.New("FLAG_SENTINEL credential_sentinel command_sentinel")}
		_, err := NewOperatorRealtimeAdapter(source).Read(context.Background(), principal, tournamentID, nil)
		if !errors.Is(err, ErrOperatorRealtimeSource) {
			t.Fatalf("Read() error = %v", err)
		}
		operatorRealtimeRequireSanitizedError(t, err, "flag_sentinel", "credential_sentinel", "command_sentinel")
	})
}

type operatorRealtimeSourceFake struct {
	state             OperatorRealtimeState
	err               error
	query             OperatorRealtimeQuery
	reads             int
	mutations         int
	mutateQueryCursor bool
}

func (s *operatorRealtimeSourceFake) ReadOperatorRealtime(_ context.Context, query OperatorRealtimeQuery) (OperatorRealtimeState, error) {
	s.reads++
	s.query = query
	if query.Cursor != nil {
		cursor := *query.Cursor
		s.query.Cursor = &cursor
		if s.mutateQueryCursor {
			query.Cursor.LastSequence = 999
			query.Cursor.ProjectionRevision = 999
		}
	}
	return s.state, s.err
}

func operatorRealtimeState(t *testing.T, tournamentID uuid.UUID, firstSequence, lastSequence int) OperatorRealtimeState {
	t.Helper()
	events := make([]RealtimeEnvelope, 0, lastSequence-firstSequence+1)
	for sequence := firstSequence; sequence <= lastSequence; sequence++ {
		events = append(events, operatorRealtimeEnvelope(t, tournamentID, int64(sequence), int64(sequence)))
	}
	snapshotSequence := int64(lastSequence + 1)
	return OperatorRealtimeState{
		Available: RealtimeAvailableRange{OldestSequence: int64(firstSequence), LatestSequence: int64(lastSequence), CurrentProjectionRevision: int64(lastSequence)},
		Events:    events,
		Snapshot:  operatorRealtimeEnvelope(t, tournamentID, snapshotSequence, snapshotSequence),
	}
}

func operatorRealtimeEnvelope(t *testing.T, tournamentID uuid.UUID, sequence, revision int64) RealtimeEnvelope {
	t.Helper()
	input := testOperatorSnapshotInput(tournamentID)
	input.LastSequence = sequence
	input.Revision = revision
	input.CorrectionRevision = revision
	snapshot, err := NewOperatorSnapshot(OperatorSnapshotAccess{
		Authenticated: true,
		TournamentID:  tournamentID,
		OperatorID:    testUUID("b0000000-0000-4000-8000-000000000003"),
	}, input)
	if err != nil {
		t.Fatalf("NewOperatorSnapshot() error = %v", err)
	}
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      ArenaRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           sequence,
		EventID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("operator/%s/%d/%d", tournamentID, sequence, revision))),
		OccurredAt:         time.Date(2026, 9, 2, 9, 0, 0, int(sequence), time.UTC),
		ProjectionRevision: revision,
	}, snapshot)
	if err != nil {
		t.Fatalf("NewRealtimeEnvelope() error = %v", err)
	}
	return envelope
}

func operatorRealtimeRequireSanitizedError(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	lower := strings.ToLower(err.Error())
	for _, value := range forbidden {
		if strings.Contains(lower, strings.ToLower(value)) {
			t.Fatalf("error leaks %q: %v", value, err)
		}
	}
}
