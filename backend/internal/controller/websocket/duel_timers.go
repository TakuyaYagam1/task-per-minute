package websocket

import (
	"time"

	"github.com/google/uuid"

	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

type pauseableDuelTimers struct {
	deadline duelusecase.DuelTimer
	hints    *duelusecase.HintScheduler
}

func NewPauseableDuelTimers(deadline duelusecase.DuelTimer, hints *duelusecase.HintScheduler) duelusecase.DuelTimer {
	return &pauseableDuelTimers{
		deadline: deadline,
		hints:    hints,
	}
}

func (t *pauseableDuelTimers) Start(duelID uuid.UUID, deadline time.Time, onExpire func()) {
	if t == nil || t.deadline == nil {
		return
	}
	t.deadline.Start(duelID, deadline, onExpire)
}

func (t *pauseableDuelTimers) Stop(duelID uuid.UUID) bool {
	stopped := false
	if t != nil && t.deadline != nil {
		stopped = t.deadline.Stop(duelID)
	}
	if t != nil && t.hints != nil {
		stopped = t.hints.StopDuel(duelID) || stopped
	}
	return stopped
}

func (t *pauseableDuelTimers) Freeze(duelID uuid.UUID, pausedAt time.Time) bool {
	frozen := false
	if t != nil && t.deadline != nil {
		frozen = t.deadline.Freeze(duelID, pausedAt)
	}
	if t != nil && t.hints != nil && frozen {
		t.hints.Freeze(duelID, pausedAt)
	}
	return frozen
}

func (t *pauseableDuelTimers) Resume(duelID uuid.UUID, resumedAt time.Time) (time.Time, bool) {
	if t == nil || t.deadline == nil {
		return time.Time{}, false
	}
	deadline, ok := t.deadline.Resume(duelID, resumedAt)
	if ok && t.hints != nil {
		t.hints.Resume(duelID, resumedAt)
	}
	return deadline, ok
}
