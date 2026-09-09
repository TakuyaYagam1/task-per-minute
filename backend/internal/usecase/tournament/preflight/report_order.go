package preflight

import (
	"bytes"
	"sort"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func normalizeSwissPairs(pairs []swissusecase.Pair) []swissusecase.Pair {
	result := append([]swissusecase.Pair{}, pairs...)
	for i := range result {
		result[i] = normalizeSwissPair(result[i])
	}
	sort.Slice(result, func(i, j int) bool {
		return swissPairLess(result[i], result[j])
	})
	return result
}

func normalizeSwissPair(pair swissusecase.Pair) swissusecase.Pair {
	if bytes.Compare(pair.FirstParticipantID[:], pair.SecondParticipantID[:]) > 0 {
		pair.FirstParticipantID, pair.SecondParticipantID = pair.SecondParticipantID, pair.FirstParticipantID
	}
	return pair
}

func swissPairLess(first, second swissusecase.Pair) bool {
	if comparison := bytes.Compare(first.FirstParticipantID[:], second.FirstParticipantID[:]); comparison != 0 {
		return comparison < 0
	}
	return bytes.Compare(first.SecondParticipantID[:], second.SecondParticipantID[:]) < 0
}

func structuralParticipantLess(first, second Participant) bool {
	if comparison := bytes.Compare(first.ParticipantID[:], second.ParticipantID[:]); comparison != 0 {
		return comparison < 0
	}
	if comparison := bytes.Compare(first.PlayerID[:], second.PlayerID[:]); comparison != 0 {
		return comparison < 0
	}
	if first.Seed != second.Seed {
		return first.Seed < second.Seed
	}
	if first.Attendance != second.Attendance {
		return first.Attendance < second.Attendance
	}
	return bytes.Compare(first.ReservedTournamentID[:], second.ReservedTournamentID[:]) < 0
}

func structuralOverrideLess(first, second OverrideEvidence) bool {
	if first.Pair != second.Pair {
		return swissPairLess(first.Pair, second.Pair)
	}
	if comparison := bytes.Compare(first.ActorID[:], second.ActorID[:]); comparison != 0 {
		return comparison < 0
	}
	if first.Confirmed != second.Confirmed {
		return !first.Confirmed && second.Confirmed
	}
	return first.Reason < second.Reason
}

func taskVersionHealthLess(first, second domain.TaskVersionHealth) bool {
	if comparison := bytes.Compare(first.TaskID[:], second.TaskID[:]); comparison != 0 {
		return comparison < 0
	}
	if first.Version != second.Version {
		return first.Version < second.Version
	}
	if comparison := bytes.Compare(first.PoolRevisionID[:], second.PoolRevisionID[:]); comparison != 0 {
		return comparison < 0
	}
	if first.PoolKind != second.PoolKind {
		return first.PoolKind < second.PoolKind
	}
	return taskVersionHealthFlags(first) < taskVersionHealthFlags(second)
}

func taskVersionHealthFlags(in domain.TaskVersionHealth) string {
	flags := [...]bool{in.Exists, in.Enabled, in.Healthy, in.MutationLocked, in.PubliclyExposed}
	var result strings.Builder
	result.Grow(len(flags))
	for _, flag := range flags {
		if flag {
			result.WriteByte('1')
		} else {
			result.WriteByte('0')
		}
	}
	return result.String()
}
