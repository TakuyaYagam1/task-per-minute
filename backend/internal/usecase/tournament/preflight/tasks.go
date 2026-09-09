package preflight

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	CodeTaskPoolsValid Code = "tournament.preflight.tasks.pool_configuration"
	CodeTaskInventory  Code = "tournament.preflight.tasks.inventory"
	CodeTaskMissing    Code = "tournament.preflight.tasks.missing"
	CodeTaskDisabled   Code = "tournament.preflight.tasks.disabled"
	CodeTaskUnhealthy  Code = "tournament.preflight.tasks.unhealthy"
	CodeTaskMutable    Code = "tournament.preflight.tasks.mutable"
	CodeTaskExposed    Code = "tournament.preflight.tasks.publicly_exposed"
	CodeTaskWrongPool  Code = "tournament.preflight.tasks.wrong_pool"
)

type TaskHealthInput struct {
	NormalPool domain.TaskPoolRevision
	GoldenPool domain.TaskPoolRevision
	Versions   []domain.TaskVersionHealth
}

type taskVersionKey struct {
	taskID  uuid.UUID
	version int
}

type expectedTaskVersion struct {
	key            taskVersionKey
	poolRevisionID uuid.UUID
	poolKind       domain.AssignmentTaskKind
}

func EvaluateTaskHealth(in TaskHealthInput) Report {
	expected, poolIssues := expectedTaskVersions(in.NormalPool, in.GoldenPool)
	inventory, inventoryIssues := indexedTaskHealth(in.Versions)
	missing, disabled, unhealthy := make([]string, 0), make([]string, 0), make([]string, 0)
	mutable, exposed, wrongPool := make([]string, 0), make([]string, 0), make([]string, 0)

	for _, item := range expected {
		ref := taskVersionEvidence(item.poolKind, item.key)
		status, exists := inventory[item.key]
		if !exists || !status.Exists {
			missing = append(missing, ref)
			continue
		}
		if !status.Enabled {
			disabled = append(disabled, ref)
		}
		if !status.Healthy {
			unhealthy = append(unhealthy, ref)
		}
		if !status.MutationLocked {
			mutable = append(mutable, ref)
		}
		if status.PubliclyExposed {
			exposed = append(exposed, ref)
		}
		if status.PoolRevisionID != item.poolRevisionID || status.PoolKind != item.poolKind {
			wrongPool = append(wrongPool, ref)
		}
	}

	return Report{Checks: []Check{
		newPreflightCheck(
			CodeTaskPoolsValid,
			"Normal and Golden pools are distinct non-empty immutable revisions.",
			poolIssues,
			[]string{taskPoolEvidence(in.NormalPool), taskPoolEvidence(in.GoldenPool)},
		),
		newPreflightCheck(
			CodeTaskInventory,
			"Task health inventory has one valid record per task version.",
			inventoryIssues,
			[]string{fmt.Sprintf("records:%d", len(inventory))},
		),
		newPreflightCheck(
			CodeTaskMissing,
			"Every configured task version exists.",
			missing,
			[]string{fmt.Sprintf("found:%d", len(expected))},
		),
		newPreflightCheck(
			CodeTaskDisabled,
			"Every configured task version is enabled.",
			disabled,
			[]string{fmt.Sprintf("enabled:%d", len(expected))},
		),
		newPreflightCheck(
			CodeTaskUnhealthy,
			"Every configured task version passes internal health checks.",
			unhealthy,
			[]string{fmt.Sprintf("healthy:%d", len(expected))},
		),
		newPreflightCheck(
			CodeTaskMutable,
			"Every configured task version has an immutable mutation lock.",
			mutable,
			[]string{fmt.Sprintf("locked:%d", len(expected))},
		),
		newPreflightCheck(
			CodeTaskExposed,
			"Configured task versions are absent from public exposure paths.",
			exposed,
			[]string{fmt.Sprintf("private:%d", len(expected))},
		),
		newPreflightCheck(
			CodeTaskWrongPool,
			"Every task version belongs to its configured normal or Golden pool revision.",
			wrongPool,
			[]string{fmt.Sprintf("owned:%d", len(expected))},
		),
	}}
}

func expectedTaskVersions(
	normalPool domain.TaskPoolRevision,
	goldenPool domain.TaskPoolRevision,
) ([]expectedTaskVersion, []string) {
	normalizedNormal, normalErr := domain.NormalizeTaskPoolRevision(normalPool, domain.AssignmentTaskKindNormal)
	normalizedGolden, goldenErr := domain.NormalizeTaskPoolRevision(goldenPool, domain.AssignmentTaskKindGolden)
	issues := make([]string, 0)
	if normalErr != nil {
		issues = append(issues, "normal_pool:invalid")
	}
	if goldenErr != nil {
		issues = append(issues, "golden_pool:invalid")
	}
	if normalErr == nil && goldenErr == nil && domain.TaskPoolsOverlap(normalizedNormal, normalizedGolden) {
		issues = append(issues, "pools:overlap")
	}
	expected := appendExpectedTaskVersions(nil, normalizedNormal)
	expected = appendExpectedTaskVersions(expected, normalizedGolden)
	sort.Slice(expected, func(i, j int) bool {
		comparison := bytes.Compare(expected[i].key.taskID[:], expected[j].key.taskID[:])
		if comparison != 0 {
			return comparison < 0
		}
		return expected[i].key.version < expected[j].key.version
	})
	return expected, issues
}

func appendExpectedTaskVersions(out []expectedTaskVersion, pool domain.TaskPoolRevision) []expectedTaskVersion {
	for _, version := range pool.Versions {
		out = append(out, expectedTaskVersion{
			key:            taskVersionKey{taskID: version.TaskID, version: version.Version},
			poolRevisionID: pool.ID,
			poolKind:       pool.Kind,
		})
	}
	return out
}

func indexedTaskHealth(versions []domain.TaskVersionHealth) (map[taskVersionKey]domain.TaskVersionHealth, []string) {
	indexed := make(map[taskVersionKey]domain.TaskVersionHealth, len(versions))
	issues := make([]string, 0)
	for _, version := range versions {
		if version.TaskID == uuid.Nil || version.Version < 1 {
			issues = append(issues, "record:invalid_identity")
			continue
		}
		key := taskVersionKey{taskID: version.TaskID, version: version.Version}
		if _, duplicate := indexed[key]; duplicate {
			issues = append(issues, "record:"+taskVersionEvidence(version.PoolKind, key)+":duplicate")
			continue
		}
		indexed[key] = version
	}
	return indexed, issues
}

func taskVersionEvidence(kind domain.AssignmentTaskKind, key taskVersionKey) string {
	return string(kind) + ":" + key.taskID.String() + "@" + fmt.Sprintf("%d", key.version)
}

func taskPoolEvidence(pool domain.TaskPoolRevision) string {
	return fmt.Sprintf("%s:%s:revision:%d", pool.Kind, pool.ID, pool.Revision)
}
