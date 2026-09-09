package preflight_test

import (
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestPreflightStructure(t *testing.T) {
	t.Parallel()

	wantCodes := []tournamentusecase.Code{
		tournamentusecase.CodeRosterComplete,
		tournamentusecase.CodeAttendance,
		tournamentusecase.CodeParticipantExclusive,
		tournamentusecase.CodePreset,
		tournamentusecase.CodeCategories,
		tournamentusecase.CodePairings,
		tournamentusecase.CodeByes,
		tournamentusecase.CodeOverrides,
	}

	t.Run("emits stable normalized checks", func(t *testing.T) {
		t.Parallel()

		input := validStructuralPreflightInput()
		report := tournamentusecase.EvaluateStructure(input)
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
		if other := tournamentusecase.EvaluateStructure(reordered); !reflect.DeepEqual(other, report) {
			t.Fatalf("reordered report = %+v, want %+v", other, report)
		}
	})

	t.Run("reports every structural failure code", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			code   tournamentusecase.Code
			mutate func(*tournamentusecase.StructuralInput)
		}{
			{
				name: "roster incomplete", code: tournamentusecase.CodeRosterComplete,
				mutate: func(in *tournamentusecase.StructuralInput) { in.Participants = in.Participants[:4] },
			},
			{
				name: "attendance", code: tournamentusecase.CodeAttendance,
				mutate: func(in *tournamentusecase.StructuralInput) {
					in.Participants[0].Attendance = domain.AttendanceStateRegistered
				},
			},
			{
				name: "participant exclusivity", code: tournamentusecase.CodeParticipantExclusive,
				mutate: func(in *tournamentusecase.StructuralInput) {
					in.Participants[1].PlayerID = in.Participants[0].PlayerID
				},
			},
			{
				name: "preset", code: tournamentusecase.CodePreset,
				mutate: func(in *tournamentusecase.StructuralInput) { in.Preset = domain.TournamentPreset("unknown") },
			},
			{
				name: "categories", code: tournamentusecase.CodeCategories,
				mutate: func(in *tournamentusecase.StructuralInput) {
					in.CategoryPools[0].Categories = in.CategoryPools[0].Categories[:2]
				},
			},
			{
				name: "pairings", code: tournamentusecase.CodePairings,
				mutate: func(in *tournamentusecase.StructuralInput) {
					in.Pairings[0].SecondParticipantID = in.Pairings[0].FirstParticipantID
				},
			},
			{
				name: "bye", code: tournamentusecase.CodeByes,
				mutate: func(in *tournamentusecase.StructuralInput) {
					in.ByeParticipantID = in.Pairings[0].FirstParticipantID
				},
			},
			{
				name: "override", code: tournamentusecase.CodeOverrides,
				mutate: func(in *tournamentusecase.StructuralInput) { in.Overrides[0].Confirmed = false },
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				input := validStructuralPreflightInput()
				test.mutate(&input)
				report := tournamentusecase.EvaluateStructure(input)
				check := preflightCheck(t, report, test.code)
				if check.Passed || len(check.Evidence) == 0 || report.Passed() {
					t.Fatalf("check = %+v, report passed = %v", check, report.Passed())
				}
			})
		}
	})
}

func validStructuralPreflightInput() tournamentusecase.StructuralInput {
	content := preflightContentInput()
	participants := make([]tournamentusecase.Participant, 5)
	for i := range participants {
		participants[i] = tournamentusecase.Participant{
			ParticipantID:        preflightContentID(40 + i),
			PlayerID:             preflightContentID(50 + i),
			Seed:                 i + 1,
			Attendance:           domain.AttendanceStateCheckedIn,
			ReservedTournamentID: content.TournamentID,
		}
	}
	repeated := swissusecase.Pair{
		FirstParticipantID:  participants[0].ParticipantID,
		SecondParticipantID: participants[1].ParticipantID,
	}
	return tournamentusecase.StructuralInput{
		TournamentID:       content.TournamentID,
		Preset:             domain.TournamentPresetV1,
		ExpectedRosterSize: len(participants),
		Participants:       participants,
		CategoryPools:      content.CategoryPools,
		Pairings: []swissusecase.Pair{
			repeated,
			{FirstParticipantID: participants[2].ParticipantID, SecondParticipantID: participants[3].ParticipantID},
		},
		ByeParticipantID: participants[4].ParticipantID,
		RepeatedPairings: []swissusecase.Pair{repeated},
		Overrides: []tournamentusecase.OverrideEvidence{
			{Pair: repeated, ActorID: preflightContentID(80), Confirmed: true, Reason: "No complete alternative exists"},
		},
	}
}

func cloneStructuralPreflightInput(in tournamentusecase.StructuralInput) tournamentusecase.StructuralInput {
	cloned := in
	cloned.Participants = append([]tournamentusecase.Participant(nil), in.Participants...)
	cloned.CategoryPools = make([]domain.CategoryPoolRevision, len(in.CategoryPools))
	for i, pool := range in.CategoryPools {
		cloned.CategoryPools[i] = pool
		cloned.CategoryPools[i].Categories = append([]domain.Category(nil), pool.Categories...)
	}
	cloned.Pairings = append([]swissusecase.Pair(nil), in.Pairings...)
	cloned.RepeatedPairings = append([]swissusecase.Pair(nil), in.RepeatedPairings...)
	cloned.Overrides = append([]tournamentusecase.OverrideEvidence(nil), in.Overrides...)
	return cloned
}

func preflightCodes(report tournamentusecase.Report) []tournamentusecase.Code {
	codes := make([]tournamentusecase.Code, len(report.Checks))
	for i, check := range report.Checks {
		codes[i] = check.Code
	}
	return codes
}

func preflightCheck(
	t *testing.T,
	report tournamentusecase.Report,
	code tournamentusecase.Code,
) tournamentusecase.Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Code == code {
			return check
		}
	}
	t.Fatalf("preflight check %q not found", code)
	return tournamentusecase.Check{}
}
