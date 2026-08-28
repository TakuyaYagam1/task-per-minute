package arena_test

import (
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestPreflightStructure(t *testing.T) {
	t.Parallel()

	wantCodes := []arena.ArenaPreflightCode{
		arena.PreflightCodeRosterComplete,
		arena.PreflightCodeAttendance,
		arena.PreflightCodeParticipantExclusive,
		arena.PreflightCodePreset,
		arena.PreflightCodeCategories,
		arena.PreflightCodePairings,
		arena.PreflightCodeByes,
		arena.PreflightCodeOverrides,
	}

	t.Run("emits stable normalized checks", func(t *testing.T) {
		t.Parallel()

		input := validStructuralPreflightInput()
		report := arena.EvaluateStructuralPreflight(input)
		if !report.Passed() {
			t.Fatalf("valid report failed: %+v", report)
		}
		if got := preflightCodes(report); !reflect.DeepEqual(got, wantCodes) {
			t.Fatalf("codes = %v, want %v", got, wantCodes)
		}
		for _, check := range report.Checks {
			if check.Explanation == "" || len(check.Evidence) == 0 {
				t.Fatalf("check lacks explanation or evidence: %+v", check)
			}
			if !sort.StringsAreSorted(check.Evidence) {
				t.Fatalf("evidence is not normalized: %v", check.Evidence)
			}
		}

		reordered := cloneStructuralPreflightInput(input)
		slices.Reverse(reordered.Participants)
		slices.Reverse(reordered.Pairings)
		slices.Reverse(reordered.CategoryPools)
		for i := range reordered.CategoryPools {
			slices.Reverse(reordered.CategoryPools[i].Categories)
		}
		if other := arena.EvaluateStructuralPreflight(reordered); !reflect.DeepEqual(other, report) {
			t.Fatalf("reordered report = %+v, want %+v", other, report)
		}
	})

	t.Run("reports every structural failure code", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			code   arena.ArenaPreflightCode
			mutate func(*arena.StructuralPreflightInput)
		}{
			{
				name: "roster incomplete", code: arena.PreflightCodeRosterComplete,
				mutate: func(in *arena.StructuralPreflightInput) { in.Participants = in.Participants[:4] },
			},
			{
				name: "attendance", code: arena.PreflightCodeAttendance,
				mutate: func(in *arena.StructuralPreflightInput) {
					in.Participants[0].Attendance = domain.ArenaAttendanceStateRegistered
				},
			},
			{
				name: "participant exclusivity", code: arena.PreflightCodeParticipantExclusive,
				mutate: func(in *arena.StructuralPreflightInput) {
					in.Participants[1].PlayerID = in.Participants[0].PlayerID
				},
			},
			{
				name: "preset", code: arena.PreflightCodePreset,
				mutate: func(in *arena.StructuralPreflightInput) { in.Preset = domain.ArenaPreset("unknown") },
			},
			{
				name: "categories", code: arena.PreflightCodeCategories,
				mutate: func(in *arena.StructuralPreflightInput) {
					in.CategoryPools[0].Categories = in.CategoryPools[0].Categories[:2]
				},
			},
			{
				name: "pairings", code: arena.PreflightCodePairings,
				mutate: func(in *arena.StructuralPreflightInput) {
					in.Pairings[0].SecondParticipantID = in.Pairings[0].FirstParticipantID
				},
			},
			{
				name: "bye", code: arena.PreflightCodeByes,
				mutate: func(in *arena.StructuralPreflightInput) {
					in.ByeParticipantID = in.Pairings[0].FirstParticipantID
				},
			},
			{
				name: "override", code: arena.PreflightCodeOverrides,
				mutate: func(in *arena.StructuralPreflightInput) { in.Overrides[0].Confirmed = false },
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				input := validStructuralPreflightInput()
				test.mutate(&input)
				report := arena.EvaluateStructuralPreflight(input)
				check := preflightCheck(t, report, test.code)
				if check.Passed || len(check.Evidence) == 0 || report.Passed() {
					t.Fatalf("check = %+v, report passed = %v", check, report.Passed())
				}
			})
		}
	})
}

func validStructuralPreflightInput() arena.StructuralPreflightInput {
	content := task020ContentInput()
	participants := make([]arena.StructuralParticipant, 5)
	for i := range participants {
		participants[i] = arena.StructuralParticipant{
			ParticipantID:        task020ID(40 + i),
			PlayerID:             task020ID(50 + i),
			Seed:                 i + 1,
			Attendance:           domain.ArenaAttendanceStateCheckedIn,
			ReservedTournamentID: content.TournamentID,
		}
	}
	repeated := arena.SwissPair{
		FirstParticipantID:  participants[0].ParticipantID,
		SecondParticipantID: participants[1].ParticipantID,
	}
	return arena.StructuralPreflightInput{
		TournamentID:       content.TournamentID,
		Preset:             domain.ArenaPresetV1,
		ExpectedRosterSize: len(participants),
		Participants:       participants,
		CategoryPools:      content.CategoryPools,
		Pairings: []arena.SwissPair{
			repeated,
			{FirstParticipantID: participants[2].ParticipantID, SecondParticipantID: participants[3].ParticipantID},
		},
		ByeParticipantID: participants[4].ParticipantID,
		RepeatedPairings: []arena.SwissPair{repeated},
		Overrides: []arena.StructuralOverrideEvidence{
			{Pair: repeated, ActorID: task020ID(80), Confirmed: true, Reason: "No complete alternative exists"},
		},
	}
}

func cloneStructuralPreflightInput(in arena.StructuralPreflightInput) arena.StructuralPreflightInput {
	cloned := in
	cloned.Participants = append([]arena.StructuralParticipant(nil), in.Participants...)
	cloned.CategoryPools = make([]arena.CategoryPoolRevision, len(in.CategoryPools))
	for i, pool := range in.CategoryPools {
		cloned.CategoryPools[i] = pool
		cloned.CategoryPools[i].Categories = append([]domain.Category(nil), pool.Categories...)
	}
	cloned.Pairings = append([]arena.SwissPair(nil), in.Pairings...)
	cloned.RepeatedPairings = append([]arena.SwissPair(nil), in.RepeatedPairings...)
	cloned.Overrides = append([]arena.StructuralOverrideEvidence(nil), in.Overrides...)
	return cloned
}

func preflightCodes(report arena.ArenaPreflightReport) []arena.ArenaPreflightCode {
	codes := make([]arena.ArenaPreflightCode, len(report.Checks))
	for i, check := range report.Checks {
		codes[i] = check.Code
	}
	return codes
}

func preflightCheck(
	t *testing.T,
	report arena.ArenaPreflightReport,
	code arena.ArenaPreflightCode,
) arena.ArenaPreflightCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Code == code {
			return check
		}
	}
	t.Fatalf("preflight check %q not found", code)
	return arena.ArenaPreflightCheck{}
}
