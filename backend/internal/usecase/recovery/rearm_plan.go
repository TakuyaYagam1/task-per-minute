package recovery

import (
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type RecoveryRearmPlan struct {
	TournamentID uuid.UUID
	Cursor       RecoveryCursor
	Lease        RecoveryLease
	Work         []RecoveryDeadlineEvidence
}

func (i recoveryIndex) expectedWork(graph RecoveryGraph) []RecoveryWork {
	work := make([]RecoveryWork, 0, len(i.games)+len(i.waves))
	for _, wave := range graph.Waves {
		if (wave.State != domain.WaveStateReadyWindowOpen &&
			wave.State != domain.WaveStateReady) || wave.ReadyWindow == nil {
			continue
		}
		work = append(work, RecoveryWork{
			Kind: RecoveryWorkReadyWindow, TournamentID: graph.TournamentID,
			WaveID: wave.ID, ReadyWindowID: wave.ReadyWindow.ID,
		})
	}
	for _, child := range i.games {
		if child.game.State != domain.GameStateActive {
			continue
		}
		work = append(work, RecoveryWork{
			Kind: RecoveryWorkGame, TournamentID: graph.TournamentID,
			WaveID: child.waveID, SeriesID: child.seriesID, SlotID: child.slotID,
			GameID: child.game.ID,
		})
	}
	sortRecoveryWork(work)
	return work
}

type recoveryEvidenceStatus int

const (
	recoveryEvidenceValid recoveryEvidenceStatus = iota
	recoveryEvidenceMissing
	recoveryEvidenceInvalid
)

func matchRecoveryDeadlines(
	evidence []RecoveryDeadlineEvidence,
	expected []RecoveryWork,
) ([]RecoveryDeadlineEvidence, recoveryEvidenceStatus) {
	byWork := make(map[RecoveryWork]RecoveryDeadlineEvidence, len(evidence))
	for _, item := range evidence {
		if !validRecoveryWork(item.Work) || item.Deadline.IsZero() ||
			item.Deadline.Location() != time.UTC {
			return nil, recoveryEvidenceInvalid
		}
		if _, exists := byWork[item.Work]; exists {
			return nil, recoveryEvidenceInvalid
		}
		byWork[item.Work] = item
	}
	matched := make([]RecoveryDeadlineEvidence, 0, len(expected))
	for _, work := range expected {
		item, exists := byWork[work]
		if !exists {
			return nil, recoveryEvidenceMissing
		}
		matched = append(matched, item)
		delete(byWork, work)
	}
	if len(byWork) != 0 {
		return nil, recoveryEvidenceInvalid
	}
	return matched, recoveryEvidenceValid
}

func sortRecoveryWork(work []RecoveryWork) {
	sort.Slice(work, func(left, right int) bool {
		return recoveryWorkKey(work[left]) < recoveryWorkKey(work[right])
	})
}

func recoveryWorkKey(work RecoveryWork) string {
	return string(work.Kind) + "/" + work.WaveID.String() + "/" + work.SeriesID.String() +
		"/" + work.SlotID.String() + "/" + work.GameID.String() + "/" + work.ReadyWindowID.String()
}
