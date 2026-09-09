package preflight

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func newPreflightProofDocument(
	algorithmVersion string,
	tournamentID uuid.UUID,
	normalizedInputs []string,
	revisions []SourceRevision,
	checks []Check,
) preflightProofDocument {
	proofChecks := make([]preflightProofCheck, 0, len(checks))
	for _, check := range checks {
		proofChecks = append(proofChecks, preflightProofCheck{
			Code:        string(check.Code),
			Passed:      check.Passed,
			Explanation: check.Explanation,
			Evidence:    append([]string{}, check.Evidence...),
		})
	}
	return preflightProofDocument{
		AlgorithmVersion: algorithmVersion,
		TournamentID:     tournamentID.String(),
		NormalizedInputs: append([]string{}, normalizedInputs...),
		Revisions:        append([]SourceRevision{}, revisions...),
		Checks:           proofChecks,
	}
}

func preflightNormalizedInputs(in ReportInput) ([]string, error) {
	structural, err := json.Marshal(newPreflightStructuralDocument(in.Structural))
	if err != nil {
		return nil, preflightReportError("cannot encode normalized structural input")
	}
	tasks, err := json.Marshal(newPreflightTaskHealthDocument(in.TaskHealth))
	if err != nil {
		return nil, preflightReportError("cannot encode normalized task input")
	}
	runtime, err := json.Marshal(newPreflightRuntimeDocument(in.Runtime))
	if err != nil {
		return nil, preflightReportError("cannot encode normalized runtime input")
	}
	result := []string{
		"pairing_revision:" + strconv.FormatInt(in.PairingRevision, 10),
		"roster_revision:" + strconv.FormatInt(in.RosterRevision, 10),
		"runtime:" + string(runtime),
		"runtime.capacity.certified_at_utc:" + strconv.FormatBool(in.Runtime.Capacity != nil && validRuntimeTime(in.Runtime.Capacity.CertifiedAt)),
		"runtime.clock.observed_at_utc:" + strconv.FormatBool(validRuntimeTime(in.Runtime.Clock.ObservedAt)),
		"runtime.clock.reference_at_utc:" + strconv.FormatBool(validRuntimeTime(in.Runtime.Clock.ReferenceAt)),
		"runtime.schedule.must_finish_by_utc:" + strconv.FormatBool(validRuntimeTime(in.Runtime.Schedule.MustFinishBy)),
		"runtime.schedule.starts_at_utc:" + strconv.FormatBool(validRuntimeTime(in.Runtime.Schedule.StartsAt)),
		"structural:" + string(structural),
		"tasks:" + string(tasks),
	}
	sort.Strings(result)
	return result, nil
}

func newPreflightStructuralDocument(in StructuralInput) preflightStructuralDocument {
	participants := make([]preflightParticipantDocument, 0, len(in.Participants))
	for _, participant := range in.Participants {
		participants = append(participants, preflightParticipantDocument{
			ParticipantID:        participant.ParticipantID.String(),
			PlayerID:             participant.PlayerID.String(),
			Seed:                 participant.Seed,
			Attendance:           string(participant.Attendance),
			ReservedTournamentID: participant.ReservedTournamentID.String(),
		})
	}
	categoryPools := make([]preflightCategoryPoolDocument, 0, len(in.CategoryPools))
	for _, pool := range in.CategoryPools {
		categories := make([]string, 0, len(pool.Categories))
		for _, category := range pool.Categories {
			categories = append(categories, string(category))
		}
		categoryPools = append(categoryPools, preflightCategoryPoolDocument{
			ID:         pool.ID.String(),
			Revision:   pool.Revision,
			Format:     string(pool.Format),
			Categories: categories,
		})
	}
	overrides := make([]preflightOverrideEvidenceDocument, 0, len(in.Overrides))
	for _, override := range in.Overrides {
		overrides = append(overrides, preflightOverrideEvidenceDocument{
			Pair:      newPreflightPairDocument(override.Pair),
			ActorID:   override.ActorID.String(),
			Confirmed: override.Confirmed,
			Reason:    override.Reason,
		})
	}
	return preflightStructuralDocument{
		TournamentID:       in.TournamentID.String(),
		Preset:             in.Preset.String(),
		ExpectedRosterSize: in.ExpectedRosterSize,
		Participants:       participants,
		CategoryPools:      categoryPools,
		Pairings:           newPreflightPairDocuments(in.Pairings),
		ByeParticipantID:   in.ByeParticipantID.String(),
		RepeatedPairings:   newPreflightPairDocuments(in.RepeatedPairings),
		Overrides:          overrides,
	}
}

func newPreflightPairDocuments(pairs []swissusecase.Pair) []preflightPairDocument {
	result := make([]preflightPairDocument, 0, len(pairs))
	for _, pair := range pairs {
		result = append(result, newPreflightPairDocument(pair))
	}
	return result
}

func newPreflightPairDocument(pair swissusecase.Pair) preflightPairDocument {
	return preflightPairDocument{
		FirstParticipantID:  pair.FirstParticipantID.String(),
		SecondParticipantID: pair.SecondParticipantID.String(),
	}
}

func newPreflightTaskHealthDocument(in TaskHealthInput) preflightTaskHealthInputDocument {
	versions := make([]preflightTaskVersionHealthDocument, 0, len(in.Versions))
	for _, version := range in.Versions {
		versions = append(versions, preflightTaskVersionHealthDocument{
			TaskID:          version.TaskID.String(),
			Version:         version.Version,
			PoolRevisionID:  version.PoolRevisionID.String(),
			PoolKind:        string(version.PoolKind),
			Exists:          version.Exists,
			Enabled:         version.Enabled,
			Healthy:         version.Healthy,
			MutationLocked:  version.MutationLocked,
			PubliclyExposed: version.PubliclyExposed,
		})
	}
	return preflightTaskHealthInputDocument{
		NormalPool: newPreflightTaskPoolDocument(in.NormalPool),
		GoldenPool: newPreflightTaskPoolDocument(in.GoldenPool),
		Versions:   versions,
	}
}

func newPreflightTaskPoolDocument(in domain.TaskPoolRevision) preflightTaskPoolDocument {
	versions := make([]preflightTaskVersionDocument, 0, len(in.Versions))
	for _, version := range in.Versions {
		versions = append(versions, preflightTaskVersionDocument{
			TaskID:  version.TaskID.String(),
			Version: version.Version,
		})
	}
	return preflightTaskPoolDocument{
		ID:       in.ID.String(),
		Revision: in.Revision,
		Kind:     string(in.Kind),
		Versions: versions,
	}
}

func newPreflightRuntimeDocument(in RuntimeInput) preflightRuntimeDocument {
	dependencies := make([]preflightDependencyDocument, 0, len(in.Dependencies))
	for _, dependency := range in.Dependencies {
		dependencies = append(dependencies, preflightDependencyDocument{
			Name:     string(dependency.Name),
			Healthy:  dependency.Healthy,
			Revision: dependency.Revision,
		})
	}
	return preflightRuntimeDocument{
		TournamentID:    in.TournamentID.String(),
		Preset:          in.Preset.String(),
		RosterSize:      in.RosterSize,
		ContentRevision: in.ContentRevision,
		Configuration: preflightRuntimeConfigurationDocument{
			Valid:    in.Configuration.Valid,
			Revision: in.Configuration.Revision,
		},
		Health: preflightRuntimeHealthDocument{
			AuthoritativeStorage: newPreflightRuntimeComponentDocument(in.Health.AuthoritativeStorage),
			Submission:           newPreflightRuntimeComponentDocument(in.Health.Submission),
			TaskDelivery:         newPreflightRuntimeComponentDocument(in.Health.TaskDelivery),
			Realtime:             newPreflightRuntimeComponentDocument(in.Health.Realtime),
		},
		Capacity: newPreflightCapacityDocument(in.Capacity),
		Clock: preflightClockDocument{
			ObservedAt:  canonicalPreflightTime(in.Clock.ObservedAt),
			ReferenceAt: canonicalPreflightTime(in.Clock.ReferenceAt),
			MaxSkew:     int64(in.Clock.MaxSkew),
		},
		Dependencies: dependencies,
		Schedule: preflightScheduleDocument{
			StartsAt:          canonicalPreflightTime(in.Schedule.StartsAt),
			MustFinishBy:      canonicalPreflightTime(in.Schedule.MustFinishBy),
			ProjectedDuration: int64(in.Schedule.ProjectedDuration),
		},
	}
}

func newPreflightRuntimeComponentDocument(in ComponentHealth) preflightRuntimeComponentDocument {
	return preflightRuntimeComponentDocument{Healthy: in.Healthy, Revision: in.Revision}
}

func newPreflightCapacityDocument(in *Certification) *preflightCapacityDocument {
	if in == nil {
		return nil
	}
	return &preflightCapacityDocument{
		ID:                   in.ID.String(),
		TournamentID:         in.TournamentID.String(),
		ContentRevision:      in.ContentRevision,
		RosterSize:           in.RosterSize,
		NormalPoolRevisionID: in.NormalPoolRevisionID.String(),
		NormalPoolRevision:   in.NormalPoolRevision,
		GoldenPoolRevisionID: in.GoldenPoolRevisionID.String(),
		GoldenPoolRevision:   in.GoldenPoolRevision,
		NormalProofDigest:    in.NormalProofDigest,
		GoldenProofDigest:    in.GoldenProofDigest,
		CertifiedAt:          canonicalPreflightTime(in.CertifiedAt),
	}
}

func canonicalPreflightTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}
