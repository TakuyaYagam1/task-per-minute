package duel

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

type HintSender func(playerID uuid.UUID, event HintUnlocked)

type HintUnlocked struct {
	DuelID uuid.UUID
	TaskID uuid.UUID
	Hint   domain.UnlockedHint
}

type HintSnapshot struct {
	Task     *domain.Task
	Schedule []domain.HintScheduleEntry
	Unlocked []domain.UnlockedHint
}

type HintScheduler struct {
	mu     sync.Mutex
	clock  clock.Clock
	send   HintSender
	states map[uuid.UUID]*hintDuelState
}

type hintDuelState struct {
	players map[uuid.UUID]*hintPlayerState
}

type hintPlayerState struct {
	playerID  uuid.UUID
	duelID    uuid.UUID
	task      *domain.Task
	schedule  []domain.HintScheduleEntry
	unlocked  []domain.UnlockedHint
	timers    [domain.TaskHintCount]*time.Timer
	remaining [domain.TaskHintCount]time.Duration
	paused    bool
	stopped   bool
}

func NewHintScheduler(clk clock.Clock, send HintSender) *HintScheduler {
	if clk == nil {
		clk = clock.Real{}
	}
	return &HintScheduler{
		clock:  clk,
		send:   send,
		states: make(map[uuid.UUID]*hintDuelState),
	}
}

func (s *HintScheduler) SetSender(send HintSender) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.send = send
}

func (s *HintScheduler) StartDuel(duel *domain.Duel, assignments map[uuid.UUID]*domain.Task) {
	if s == nil || duel == nil || len(assignments) == 0 {
		return
	}

	s.StopDuel(duel.ID)

	now := s.clock.Now()
	state := &hintDuelState{players: make(map[uuid.UUID]*hintPlayerState, len(assignments))}

	s.mu.Lock()
	s.states[duel.ID] = state
	for playerID, task := range assignments {
		if task == nil {
			continue
		}
		normalizedHints, ok := domain.NormalizeTaskHints(task.Hints)
		if !ok {
			continue
		}
		task = cloneTask(task)
		task.Hints = normalizedHints
		player := &hintPlayerState{
			playerID: playerID,
			duelID:   duel.ID,
			task:     task,
			schedule: domain.BuildHintSchedule(duel.StartedAt, task.TimeLimit),
		}
		state.players[playerID] = player
		for idx := range domain.TaskHintCount {
			if _, ok := domain.TaskHintText(player.task.Hints, idx); !ok {
				continue
			}
			delay := player.schedule[idx].UnlockAt.Sub(now)
			if delay < 0 {
				delay = 0
			}
			hintIndex := idx
			player.timers[idx] = time.AfterFunc(delay, func() {
				s.unlock(duel.ID, playerID, hintIndex)
			})
		}
	}
	s.mu.Unlock()
}

func (s *HintScheduler) StopAll() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for duelID, state := range s.states {
		for _, player := range state.players {
			player.stopped = true
			for _, timer := range player.timers {
				if timer != nil {
					timer.Stop()
				}
			}
		}
		delete(s.states, duelID)
	}
}

func (s *HintScheduler) StopDuel(duelID uuid.UUID) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.states[duelID]
	if !ok {
		return false
	}
	for _, player := range state.players {
		player.stopped = true
		for _, timer := range player.timers {
			if timer != nil {
				timer.Stop()
			}
		}
	}
	delete(s.states, duelID)
	return true
}

func (s *HintScheduler) Freeze(duelID uuid.UUID, pausedAt time.Time) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.states[duelID]
	if !ok {
		return false
	}
	for _, player := range state.players {
		if player.stopped || player.paused {
			continue
		}
		player.paused = true
		for idx, schedule := range player.schedule {
			if _, ok := domain.TaskHintText(player.task.Hints, idx); !ok {
				continue
			}
			if player.isUnlocked(idx) {
				continue
			}
			if player.timers[idx] != nil {
				player.timers[idx].Stop()
			}
			player.remaining[idx] = schedule.UnlockAt.Sub(pausedAt)
			if player.remaining[idx] < 0 {
				player.remaining[idx] = 0
			}
		}
	}
	return true
}

func (s *HintScheduler) Resume(duelID uuid.UUID, resumedAt time.Time) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.states[duelID]
	if !ok {
		return false
	}
	for _, player := range state.players {
		if player.stopped || !player.paused {
			continue
		}
		player.paused = false
		for idx := range domain.TaskHintCount {
			if _, ok := domain.TaskHintText(player.task.Hints, idx); !ok {
				continue
			}
			if player.isUnlocked(idx) {
				continue
			}
			delay := player.remaining[idx]
			player.remaining[idx] = 0
			player.schedule[idx].UnlockAt = resumedAt.Add(delay)
			hintIndex := idx
			player.timers[idx] = time.AfterFunc(delay, func() {
				s.unlock(duelID, player.playerID, hintIndex)
			})
		}
	}
	return true
}

func (s *HintScheduler) PlayerSnapshot(duelID, playerID uuid.UUID) (HintSnapshot, bool) {
	if s == nil {
		return HintSnapshot{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.states[duelID]
	if !ok {
		return HintSnapshot{}, false
	}
	player, ok := state.players[playerID]
	if !ok {
		return HintSnapshot{}, false
	}
	return HintSnapshot{
		Task:     cloneTask(player.task),
		Schedule: player.visibleSchedule(),
		Unlocked: cloneUnlocked(player.unlocked),
	}, true
}

func (s *HintScheduler) unlock(duelID, playerID uuid.UUID, idx int) {
	var send HintSender
	var event HintUnlocked

	s.mu.Lock()
	state, ok := s.states[duelID]
	if !ok {
		s.mu.Unlock()
		return
	}
	player, ok := state.players[playerID]
	if !ok || player.stopped || player.paused || idx < 0 || idx >= domain.TaskHintCount || player.isUnlocked(idx) {
		s.mu.Unlock()
		return
	}

	hint, ok := domain.TaskHintText(player.task.Hints, idx)
	if !ok {
		s.mu.Unlock()
		return
	}
	unlocked := domain.UnlockedHint{
		Index:      idx + 1,
		Text:       hint,
		UnlockedAt: player.schedule[idx].UnlockAt,
	}
	player.unlocked = append(player.unlocked, unlocked)
	send = s.send
	event = HintUnlocked{
		DuelID: duelID,
		TaskID: player.task.ID,
		Hint:   unlocked,
	}
	s.mu.Unlock()

	if send != nil {
		send(playerID, event)
	}
}

func (p *hintPlayerState) isUnlocked(idx int) bool {
	hintIndex := idx + 1
	for _, hint := range p.unlocked {
		if hint.Index == hintIndex {
			return true
		}
	}
	return false
}

func (p *hintPlayerState) visibleSchedule() []domain.HintScheduleEntry {
	out := make([]domain.HintScheduleEntry, 0, len(p.schedule))
	for idx, entry := range p.schedule {
		if _, ok := domain.TaskHintText(p.task.Hints, idx); ok {
			out = append(out, entry)
		}
	}
	return out
}

func cloneTask(task *domain.Task) *domain.Task {
	if task == nil {
		return nil
	}
	out := *task
	out.Hints = append([]string(nil), task.Hints...)
	return &out
}

func cloneUnlocked(in []domain.UnlockedHint) []domain.UnlockedHint {
	out := append([]domain.UnlockedHint(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Index < out[j].Index
	})
	return out
}
