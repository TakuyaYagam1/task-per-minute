package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestWavePreservesOneSharedStartAcrossPause(t *testing.T) {
	openedAt := time.Date(2026, time.August, 27, 9, 0, 0, 0, time.UTC)
	wave, windowID := openWave(t, openedAt)

	for _, member := range wave.Members {
		changed, err := wave.MarkReady(windowID, member.ParticipantID, openedAt.Add(time.Second))
		if err != nil || !changed {
			t.Fatalf("mark member ready: changed=%v err=%v", changed, err)
		}
	}
	startedAt := openedAt.Add(2 * time.Second)
	changed, err := wave.Start(windowID, startedAt)
	if err != nil || !changed {
		t.Fatalf("start wave: changed=%v err=%v", changed, err)
	}
	if err := wave.Pause(startedAt.Add(time.Second)); err != nil {
		t.Fatalf("pause wave: %v", err)
	}
	if err := wave.Resume(); err != nil {
		t.Fatalf("resume wave: %v", err)
	}
	if wave.StartedAt == nil || !wave.StartedAt.Equal(startedAt) {
		t.Fatalf("shared start changed: %v", wave.StartedAt)
	}
	if changed, err = wave.Start(windowID, startedAt.Add(time.Second)); !errors.Is(err, domain.ErrWaveAlreadyStarted) || changed {
		t.Fatalf("second start: changed=%v err=%v", changed, err)
	}
	if err := wave.Validate(); err != nil {
		t.Fatalf("validate wave: %v", err)
	}
}

func TestWaveRejectsStaleAndExpiredReadyWindows(t *testing.T) {
	openedAt := time.Date(2026, time.August, 27, 10, 0, 0, 0, time.UTC)
	wave, windowID := openWave(t, openedAt)
	participantID := wave.Members[0].ParticipantID

	if changed, err := wave.MarkReady(uuid.New(), participantID, openedAt.Add(time.Second)); !errors.Is(err, domain.ErrReadyWindowStale) || changed {
		t.Fatalf("stale window: changed=%v err=%v", changed, err)
	}
	if changed, err := wave.MarkReady(windowID, participantID, openedAt.Add(-time.Nanosecond)); !errors.Is(err, domain.ErrReadyWindowDeadline) || changed {
		t.Fatalf("early readiness: changed=%v err=%v", changed, err)
	}
	if changed, err := wave.MarkReady(windowID, participantID, openedAt.Add(31*time.Second)); !errors.Is(err, domain.ErrReadyWindowDeadline) || changed {
		t.Fatalf("late readiness: changed=%v err=%v", changed, err)
	}
	if err := wave.ExpireReadyWindow(windowID, openedAt.Add(31*time.Second)); err != nil {
		t.Fatalf("expire window: %v", err)
	}
	if wave.Members[0].Ready || wave.State != domain.WaveStateReadyWindowExpired {
		t.Fatalf("expired window retained readiness: %+v", wave)
	}
	if err := wave.Validate(); err != nil {
		t.Fatalf("validate expired wave: %v", err)
	}
}

func TestWaveRejectsReadinessBeforeWindow(t *testing.T) {
	wave := domain.Wave{
		ID:           uuid.New(),
		TournamentID: uuid.New(),
		RevisionID:   domain.WaveRevisionID(uuid.New()),
		State:        domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: uuid.New(), Ready: true},
			{ParticipantID: uuid.New()},
		},
	}
	if err := wave.Validate(); !errors.Is(err, domain.ErrInvalidWave) {
		t.Fatalf("partial readiness before window: %v", err)
	}
}

func TestWaveSupersessionClearsReadinessAndRequiresNewIdentities(t *testing.T) {
	openedAt := time.Date(2026, time.August, 27, 11, 0, 0, 0, time.UTC)
	wave, oldWindowID := openWave(t, openedAt)
	if changed, err := wave.MarkReady(oldWindowID, wave.Members[0].ParticipantID, openedAt.Add(time.Second)); err != nil || !changed {
		t.Fatalf("mark ready: changed=%v err=%v", changed, err)
	}

	oldWaveID := wave.ID
	oldWaveRevision := wave.RevisionID
	oldWindowRevision := wave.ReadyWindow.RevisionID
	if _, err := wave.SupersedeWithReplacement(oldWaveID, domain.WaveRevisionID(uuid.New()), uuid.New(), domain.ReadyWindowRevisionID(uuid.New()), openedAt, openedAt.Add(time.Minute)); !errors.Is(err, domain.ErrWaveReplacementReuse) {
		t.Fatalf("reused wave ID: %v", err)
	}
	if _, err := wave.SupersedeWithReplacement(uuid.New(), oldWaveRevision, uuid.New(), domain.ReadyWindowRevisionID(uuid.New()), openedAt, openedAt.Add(time.Minute)); !errors.Is(err, domain.ErrWaveReplacementReuse) {
		t.Fatalf("reused wave revision: %v", err)
	}
	if _, err := wave.SupersedeWithReplacement(uuid.New(), domain.WaveRevisionID(uuid.New()), oldWindowID, domain.ReadyWindowRevisionID(uuid.New()), openedAt, openedAt.Add(time.Minute)); !errors.Is(err, domain.ErrWaveReplacementReuse) {
		t.Fatalf("reused window ID: %v", err)
	}
	if _, err := wave.SupersedeWithReplacement(uuid.New(), domain.WaveRevisionID(uuid.New()), uuid.New(), oldWindowRevision, openedAt, openedAt.Add(time.Minute)); !errors.Is(err, domain.ErrWaveReplacementReuse) {
		t.Fatalf("reused window revision: %v", err)
	}

	newWindowID := uuid.New()
	replacement, err := wave.SupersedeWithReplacement(
		uuid.New(),
		domain.WaveRevisionID(uuid.New()),
		newWindowID,
		domain.ReadyWindowRevisionID(uuid.New()),
		openedAt.Add(time.Minute),
		openedAt.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("supersede wave: %v", err)
	}
	if wave.State != domain.WaveStateSuperseded || wave.ReadyWindow.State != domain.ReadyWindowStateSuperseded {
		t.Fatalf("old wave was not superseded: %+v", wave)
	}
	for _, member := range append(wave.Members, replacement.Members...) {
		if member.Ready {
			t.Fatalf("supersession retained readiness: %+v", member)
		}
	}
	if changed, err := replacement.MarkReady(oldWindowID, replacement.Members[0].ParticipantID, openedAt.Add(time.Minute)); !errors.Is(err, domain.ErrReadyWindowStale) || changed {
		t.Fatalf("replacement accepted stale window: changed=%v err=%v", changed, err)
	}
	if err := wave.Validate(); err != nil {
		t.Fatalf("validate old wave: %v", err)
	}
	if err := replacement.Validate(); err != nil {
		t.Fatalf("validate replacement: %v", err)
	}
}

func openWave(t *testing.T, openedAt time.Time) (domain.Wave, uuid.UUID) {
	t.Helper()
	wave := domain.Wave{
		ID:           uuid.New(),
		TournamentID: uuid.New(),
		RevisionID:   domain.WaveRevisionID(uuid.New()),
		State:        domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: uuid.New()},
			{ParticipantID: uuid.New()},
		},
	}
	windowID := uuid.New()
	if err := wave.OpenReadyWindow(windowID, domain.ReadyWindowRevisionID(uuid.New()), openedAt, openedAt.Add(30*time.Second)); err != nil {
		t.Fatalf("open ready window: %v", err)
	}
	return wave, windowID
}
