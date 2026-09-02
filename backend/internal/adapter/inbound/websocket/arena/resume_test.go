package arena

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestArenaCursorResume(t *testing.T) {
	tournamentID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	otherTournamentID := uuid.MustParse("20000000-0000-4000-8000-000000000001")
	playerID := uuid.MustParse("30000000-0000-4000-8000-000000000001")
	event11 := resumeEnvelope(t, tournamentID, playerID, 11, 11)
	event14 := resumeEnvelope(t, tournamentID, playerID, 14, 14)
	snapshot := resumeEnvelope(t, tournamentID, playerID, 14, 15)
	available := RealtimeAvailableRange{OldestSequence: 10, LatestSequence: 14, CurrentProjectionRevision: 15}

	input := CursorResumeInput{
		TournamentID: tournamentID,
		Cursor: &RealtimeCursor{
			SchemaVersion:      ArenaRealtimeSchemaVersion,
			TournamentID:       tournamentID,
			LastSequence:       10,
			ProjectionRevision: 10,
		},
		Available: available,
		Events:    []RealtimeEnvelope{event11, event14},
		Snapshot:  snapshot,
	}

	t.Run("valid cursor returns ordered events across projection-only gaps", func(t *testing.T) {
		result, err := ResumeFromCursor(input)
		if err != nil {
			t.Fatalf("ResumeFromCursor() error = %v", err)
		}
		if result.UsesSnapshot {
			t.Fatal("ResumeFromCursor() unexpectedly used snapshot")
		}
		if len(result.Envelopes) != 2 || result.Envelopes[0].Sequence != 11 || result.Envelopes[1].Sequence != 14 {
			t.Fatalf("ResumeFromCursor() sequences = %v", resumeSequences(result.Envelopes))
		}
		for _, envelope := range result.Envelopes {
			if err := envelope.Validate(); err != nil {
				t.Fatalf("returned envelope Validate() error = %v", err)
			}
		}

		result.Envelopes[0].Participant.Assignment.Task.Title = "changed result"
		if input.Events[0].Participant.Assignment.Task.Title == "changed result" {
			t.Fatal("returned batch aliases input events")
		}
		input.Events[1].Participant.Assignment.Task.Title = "changed input"
		if result.Envelopes[1].Participant.Assignment.Task.Title == "changed input" {
			t.Fatal("input events alias returned batch")
		}
	})

	t.Run("fabricated cursor inside a sequence gap uses snapshot", func(t *testing.T) {
		candidate := input
		cursor := *input.Cursor
		cursor.LastSequence = 12
		cursor.ProjectionRevision = 12
		candidate.Cursor = &cursor

		result, err := ResumeFromCursor(candidate)
		if err != nil {
			t.Fatalf("ResumeFromCursor() error = %v", err)
		}
		assertResumeSnapshot(t, result, snapshot)
	})

	t.Run("no realtime event for a newer projection returns an empty batch", func(t *testing.T) {
		candidate := input
		cursor := *input.Cursor
		cursor.LastSequence = 14
		cursor.ProjectionRevision = 14
		candidate.Cursor = &cursor

		result, err := ResumeFromCursor(candidate)
		if err != nil {
			t.Fatalf("ResumeFromCursor() error = %v", err)
		}
		if result.UsesSnapshot || len(result.Envelopes) != 0 {
			t.Fatalf("ResumeFromCursor() = snapshot %t, sequences %v", result.UsesSnapshot, resumeSequences(result.Envelopes))
		}
	})

	fallbackCases := []struct {
		name   string
		change func(*CursorResumeInput)
	}{
		{name: "missing cursor", change: func(candidate *CursorResumeInput) { candidate.Cursor = nil }},
		{name: "schema mismatch", change: func(candidate *CursorResumeInput) { candidate.Cursor.SchemaVersion++ }},
		{name: "wrong tournament", change: func(candidate *CursorResumeInput) { candidate.Cursor.TournamentID = otherTournamentID }},
		{name: "stale before available boundary", change: func(candidate *CursorResumeInput) { candidate.Cursor.LastSequence = 9 }},
		{name: "future sequence", change: func(candidate *CursorResumeInput) { candidate.Cursor.LastSequence = 15 }},
		{name: "future projection", change: func(candidate *CursorResumeInput) { candidate.Cursor.ProjectionRevision = 16 }},
		{name: "cursor projection mismatch", change: func(candidate *CursorResumeInput) {
			candidate.Cursor.LastSequence = 14
			candidate.Cursor.ProjectionRevision = 13
		}},
		{name: "empty available range", change: func(candidate *CursorResumeInput) {
			candidate.Available.OldestSequence = 0
			candidate.Available.LatestSequence = 0
		}},
		{name: "unsupported cursor", change: func(candidate *CursorResumeInput) { candidate.Cursor.LastSequence = 0 }},
		{name: "malformed event", change: func(candidate *CursorResumeInput) { candidate.Events[0].ProjectionRevision = 0 }},
		{name: "unsorted events", change: func(candidate *CursorResumeInput) { candidate.Events = []RealtimeEnvelope{event14, event11} }},
		{name: "duplicate events", change: func(candidate *CursorResumeInput) { candidate.Events = []RealtimeEnvelope{event11, event11} }},
	}
	for _, testCase := range fallbackCases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := cloneResumeInput(input)
			testCase.change(&candidate)
			result, err := ResumeFromCursor(candidate)
			if err != nil {
				t.Fatalf("ResumeFromCursor() error = %v", err)
			}
			assertResumeSnapshot(t, result, snapshot)
		})
	}

	t.Run("snapshot fallback is an immutable copy", func(t *testing.T) {
		candidate := input
		candidate.Cursor = nil
		result, err := ResumeFromCursor(candidate)
		if err != nil {
			t.Fatalf("ResumeFromCursor() error = %v", err)
		}
		result.Envelopes[0].Participant.Assignment.Task.Title = "changed result"
		if snapshot.Participant.Assignment.Task.Title == "changed result" {
			t.Fatal("snapshot fallback aliases authoritative snapshot")
		}
	})

	t.Run("stale snapshot sequence is rejected", func(t *testing.T) {
		candidate := input
		candidate.Cursor = nil
		candidate.Snapshot = resumeEnvelope(t, tournamentID, playerID, 13, 15)
		result, err := ResumeFromCursor(candidate)
		if !errors.Is(err, ErrInvalidArenaResume) || len(result.Envelopes) != 0 {
			t.Fatalf("ResumeFromCursor() = %#v, %v", result, err)
		}
	})

	t.Run("stale snapshot projection is rejected", func(t *testing.T) {
		candidate := input
		candidate.Cursor = nil
		candidate.Snapshot = resumeEnvelope(t, tournamentID, playerID, 14, 14)
		result, err := ResumeFromCursor(candidate)
		if !errors.Is(err, ErrInvalidArenaResume) || len(result.Envelopes) != 0 {
			t.Fatalf("ResumeFromCursor() = %#v, %v", result, err)
		}
	})
}

func resumeEnvelope(t *testing.T, tournamentID, playerID uuid.UUID, sequence, revision int64) RealtimeEnvelope {
	t.Helper()
	payload := ParticipantSnapshot{
		TournamentID: tournamentID,
		PlayerID:     playerID,
		Revision:     revision,
		LastSequence: sequence,
		Assignment: &ParticipantAssignment{
			AssignmentID: uuid.MustParse("40000000-0000-4000-8000-000000000001"),
			AttemptID:    uuid.MustParse("50000000-0000-4000-8000-000000000001"),
			SeriesID:     uuid.MustParse("60000000-0000-4000-8000-000000000001"),
			GameID:       uuid.MustParse("70000000-0000-4000-8000-000000000001"),
			WaveID:       uuid.MustParse("80000000-0000-4000-8000-000000000001"),
			Task: ParticipantTask{
				SnapshotID:       uuid.MustParse("90000000-0000-4000-8000-000000000001"),
				TaskID:           uuid.MustParse("a0000000-0000-4000-8000-000000000001"),
				Title:            fmt.Sprintf("task-%d", sequence),
				Category:         "web",
				Difficulty:       "easy",
				TimeLimitSeconds: 60,
			},
		},
	}
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      ArenaRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           sequence,
		EventID:            uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s/%d/%d", tournamentID, sequence, revision))),
		OccurredAt:         time.Date(2026, time.September, 2, 9, 0, 0, int(sequence), time.UTC),
		ProjectionRevision: revision,
	}, payload)
	if err != nil {
		t.Fatalf("NewRealtimeEnvelope() error = %v", err)
	}
	return envelope
}

func cloneResumeInput(input CursorResumeInput) CursorResumeInput {
	clone := input
	if input.Cursor != nil {
		cursor := *input.Cursor
		clone.Cursor = &cursor
	}
	clone.Events = append([]RealtimeEnvelope(nil), input.Events...)
	return clone
}

func assertResumeSnapshot(t *testing.T, result CursorResumeResult, want RealtimeEnvelope) {
	t.Helper()
	if !result.UsesSnapshot || len(result.Envelopes) != 1 {
		t.Fatalf("ResumeFromCursor() = snapshot %t, sequences %v", result.UsesSnapshot, resumeSequences(result.Envelopes))
	}
	if result.Envelopes[0].Sequence != want.Sequence || result.Envelopes[0].ProjectionRevision != want.ProjectionRevision {
		t.Fatalf("snapshot cursor = (%d, %d), want (%d, %d)", result.Envelopes[0].Sequence, result.Envelopes[0].ProjectionRevision, want.Sequence, want.ProjectionRevision)
	}
	if err := result.Envelopes[0].Validate(); err != nil {
		t.Fatalf("snapshot Validate() error = %v", err)
	}
}

func resumeSequences(envelopes []RealtimeEnvelope) []int64 {
	sequences := make([]int64, len(envelopes))
	for index := range envelopes {
		sequences[index] = envelopes[index].Sequence
	}
	return sequences
}
