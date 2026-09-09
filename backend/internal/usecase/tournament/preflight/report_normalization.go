package preflight

import (
	"bytes"
	"sort"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func normalizePreflightReportInput(in ReportInput) ReportInput {
	in.Structural = normalizeStructuralReportInput(in.Structural)
	in.TaskHealth = normalizeTaskHealthReportInput(in.TaskHealth)
	in.Runtime = normalizeRuntimeReportInput(in.Runtime)
	return in
}

func normalizeStructuralReportInput(in StructuralInput) StructuralInput {
	in.Preset = domain.TournamentPreset(safeTypedValue(in.Preset.String(), in.Preset.IsValid()))
	in.Participants = append([]Participant{}, in.Participants...)
	for i := range in.Participants {
		attendance := in.Participants[i].Attendance
		in.Participants[i].Attendance = domain.AttendanceState(
			safeTypedValue(string(attendance), attendance.IsValid()),
		)
	}
	sort.Slice(in.Participants, func(i, j int) bool {
		return structuralParticipantLess(in.Participants[i], in.Participants[j])
	})

	in.CategoryPools = append([]domain.CategoryPoolRevision{}, in.CategoryPools...)
	for i := range in.CategoryPools {
		format := in.CategoryPools[i].Format
		in.CategoryPools[i].Format = domain.SeriesFormat(
			safeTypedValue(string(format), format.IsValid()),
		)
		in.CategoryPools[i].Categories = append([]domain.Category{}, in.CategoryPools[i].Categories...)
		for categoryIndex := range in.CategoryPools[i].Categories {
			category := in.CategoryPools[i].Categories[categoryIndex]
			in.CategoryPools[i].Categories[categoryIndex] = domain.Category(
				safeTypedValue(string(category), category.IsValid()),
			)
		}
		sort.Slice(in.CategoryPools[i].Categories, func(first, second int) bool {
			return in.CategoryPools[i].Categories[first] < in.CategoryPools[i].Categories[second]
		})
	}
	sort.Slice(in.CategoryPools, func(i, j int) bool {
		if in.CategoryPools[i].Format != in.CategoryPools[j].Format {
			return in.CategoryPools[i].Format < in.CategoryPools[j].Format
		}
		if comparison := bytes.Compare(in.CategoryPools[i].ID[:], in.CategoryPools[j].ID[:]); comparison != 0 {
			return comparison < 0
		}
		return in.CategoryPools[i].Revision < in.CategoryPools[j].Revision
	})

	in.Pairings = normalizeSwissPairs(in.Pairings)
	in.RepeatedPairings = normalizeSwissPairs(in.RepeatedPairings)
	in.Overrides = append([]OverrideEvidence{}, in.Overrides...)
	for i := range in.Overrides {
		in.Overrides[i].Pair = normalizeSwissPair(in.Overrides[i].Pair)
		if strings.TrimSpace(in.Overrides[i].Reason) == "" {
			in.Overrides[i].Reason = ""
		} else {
			in.Overrides[i].Reason = "recorded"
		}
	}
	sort.Slice(in.Overrides, func(i, j int) bool {
		return structuralOverrideLess(in.Overrides[i], in.Overrides[j])
	})
	return in
}

func normalizeTaskHealthReportInput(in TaskHealthInput) TaskHealthInput {
	in.NormalPool = normalizeTaskPoolReportInput(in.NormalPool)
	in.GoldenPool = normalizeTaskPoolReportInput(in.GoldenPool)
	in.Versions = append([]domain.TaskVersionHealth{}, in.Versions...)
	for i := range in.Versions {
		kind := in.Versions[i].PoolKind
		in.Versions[i].PoolKind = domain.AssignmentTaskKind(safeTypedValue(string(kind), kind.IsValid()))
		in.Versions[i].InternalHealthDetail = ""
	}
	sort.Slice(in.Versions, func(i, j int) bool {
		return taskVersionHealthLess(in.Versions[i], in.Versions[j])
	})
	return in
}

func normalizeTaskPoolReportInput(in domain.TaskPoolRevision) domain.TaskPoolRevision {
	in.Kind = domain.AssignmentTaskKind(safeTypedValue(string(in.Kind), in.Kind.IsValid()))
	in.Versions = append([]domain.TaskVersionRef{}, in.Versions...)
	sort.Slice(in.Versions, func(i, j int) bool {
		if comparison := bytes.Compare(in.Versions[i].TaskID[:], in.Versions[j].TaskID[:]); comparison != 0 {
			return comparison < 0
		}
		return in.Versions[i].Version < in.Versions[j].Version
	})
	return in
}

func normalizeRuntimeReportInput(in RuntimeInput) RuntimeInput {
	in.Preset = domain.TournamentPreset(safeTypedValue(in.Preset.String(), in.Preset.IsValid()))
	in.Configuration.Revision = safeRuntimeRevision(in.Configuration.Revision)
	in.Health.AuthoritativeStorage = normalizeRuntimeComponent(in.Health.AuthoritativeStorage)
	in.Health.Submission = normalizeRuntimeComponent(in.Health.Submission)
	in.Health.TaskDelivery = normalizeRuntimeComponent(in.Health.TaskDelivery)
	in.Health.Realtime = normalizeRuntimeComponent(in.Health.Realtime)
	if in.Capacity != nil {
		capacity := *in.Capacity
		capacity.NormalProofDigest = safeCapacityProofDigest(capacity.NormalProofDigest)
		capacity.GoldenProofDigest = safeCapacityProofDigest(capacity.GoldenProofDigest)
		in.Capacity = &capacity
	}
	in.Dependencies = append([]DependencyHealth{}, in.Dependencies...)
	for i := range in.Dependencies {
		name := in.Dependencies[i].Name
		in.Dependencies[i].Name = Dependency(safeTypedValue(string(name), name.IsValid()))
		in.Dependencies[i].Revision = safeRuntimeRevision(in.Dependencies[i].Revision)
		in.Dependencies[i].InternalDetail = ""
	}
	sort.Slice(in.Dependencies, func(i, j int) bool {
		if in.Dependencies[i].Name != in.Dependencies[j].Name {
			return in.Dependencies[i].Name < in.Dependencies[j].Name
		}
		if in.Dependencies[i].Revision != in.Dependencies[j].Revision {
			return in.Dependencies[i].Revision < in.Dependencies[j].Revision
		}
		return !in.Dependencies[i].Healthy && in.Dependencies[j].Healthy
	})
	return in
}

func normalizeRuntimeComponent(in ComponentHealth) ComponentHealth {
	in.Revision = safeRuntimeRevision(in.Revision)
	in.InternalDetail = ""
	return in
}
