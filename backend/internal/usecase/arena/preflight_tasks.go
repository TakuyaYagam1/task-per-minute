package arena

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	PreflightCodeTaskPoolsValid ArenaPreflightCode = "arena.preflight.tasks.pool_configuration"
	PreflightCodeTaskInventory  ArenaPreflightCode = "arena.preflight.tasks.inventory"
	PreflightCodeTaskMissing    ArenaPreflightCode = "arena.preflight.tasks.missing"
	PreflightCodeTaskDisabled   ArenaPreflightCode = "arena.preflight.tasks.disabled"
	PreflightCodeTaskUnhealthy  ArenaPreflightCode = "arena.preflight.tasks.unhealthy"
	PreflightCodeTaskMutable    ArenaPreflightCode = "arena.preflight.tasks.mutable"
	PreflightCodeTaskExposed    ArenaPreflightCode = "arena.preflight.tasks.publicly_exposed"
	PreflightCodeTaskWrongPool  ArenaPreflightCode = "arena.preflight.tasks.wrong_pool"
)

type TaskVersionHealth struct {
	TaskID               uuid.UUID
	Version              int
	PoolRevisionID       uuid.UUID
	PoolKind             domain.ArenaTaskKind
	Exists               bool
	Enabled              bool
	Healthy              bool
	MutationLocked       bool
	PubliclyExposed      bool
	InternalHealthDetail string
}

type TaskHealthPreflightInput struct {
	NormalPool TaskPoolRevision
	GoldenPool TaskPoolRevision
	Versions   []TaskVersionHealth
}

type taskVersionKey struct {
	taskID  uuid.UUID
	version int
}

type expectedTaskVersion struct {
	key            taskVersionKey
	poolRevisionID uuid.UUID
	poolKind       domain.ArenaTaskKind
}

func EvaluateTaskHealthPreflight(in TaskHealthPreflightInput) ArenaPreflightReport {
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

	return ArenaPreflightReport{Checks: []ArenaPreflightCheck{
		newArenaPreflightCheck(
			PreflightCodeTaskPoolsValid,
			"Normal and Golden pools are distinct non-empty immutable revisions.",
			poolIssues,
			[]string{taskPoolEvidence(in.NormalPool), taskPoolEvidence(in.GoldenPool)},
		),
		newArenaPreflightCheck(
			PreflightCodeTaskInventory,
			"Task health inventory has one valid record per task version.",
			inventoryIssues,
			[]string{fmt.Sprintf("records:%d", len(inventory))},
		),
		newArenaPreflightCheck(
			PreflightCodeTaskMissing,
			"Every configured task version exists.",
			missing,
			[]string{fmt.Sprintf("found:%d", len(expected))},
		),
		newArenaPreflightCheck(
			PreflightCodeTaskDisabled,
			"Every configured task version is enabled.",
			disabled,
			[]string{fmt.Sprintf("enabled:%d", len(expected))},
		),
		newArenaPreflightCheck(
			PreflightCodeTaskUnhealthy,
			"Every configured task version passes internal health checks.",
			unhealthy,
			[]string{fmt.Sprintf("healthy:%d", len(expected))},
		),
		newArenaPreflightCheck(
			PreflightCodeTaskMutable,
			"Every configured task version has an immutable mutation lock.",
			mutable,
			[]string{fmt.Sprintf("locked:%d", len(expected))},
		),
		newArenaPreflightCheck(
			PreflightCodeTaskExposed,
			"Configured task versions are absent from public exposure paths.",
			exposed,
			[]string{fmt.Sprintf("private:%d", len(expected))},
		),
		newArenaPreflightCheck(
			PreflightCodeTaskWrongPool,
			"Every task version belongs to its configured normal or Golden pool revision.",
			wrongPool,
			[]string{fmt.Sprintf("owned:%d", len(expected))},
		),
	}}
}

func expectedTaskVersions(
	normalPool TaskPoolRevision,
	goldenPool TaskPoolRevision,
) ([]expectedTaskVersion, []string) {
	normalizedNormal, normalErr := normalizeTaskPoolRevision(normalPool, domain.ArenaTaskKindNormal)
	normalizedGolden, goldenErr := normalizeTaskPoolRevision(goldenPool, domain.ArenaTaskKindGolden)
	issues := make([]string, 0)
	if normalErr != nil {
		issues = append(issues, "normal_pool:invalid")
	}
	if goldenErr != nil {
		issues = append(issues, "golden_pool:invalid")
	}
	if normalErr == nil && goldenErr == nil && taskPoolsOverlap(normalizedNormal, normalizedGolden) {
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

func appendExpectedTaskVersions(out []expectedTaskVersion, pool TaskPoolRevision) []expectedTaskVersion {
	for _, version := range pool.Versions {
		out = append(out, expectedTaskVersion{
			key:            taskVersionKey{taskID: version.TaskID, version: version.Version},
			poolRevisionID: pool.ID,
			poolKind:       pool.Kind,
		})
	}
	return out
}

func indexedTaskHealth(versions []TaskVersionHealth) (map[taskVersionKey]TaskVersionHealth, []string) {
	indexed := make(map[taskVersionKey]TaskVersionHealth, len(versions))
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

func taskVersionEvidence(kind domain.ArenaTaskKind, key taskVersionKey) string {
	return string(kind) + ":" + key.taskID.String() + "@" + fmt.Sprintf("%d", key.version)
}

func taskPoolEvidence(pool TaskPoolRevision) string {
	return fmt.Sprintf("%s:%s:revision:%d", pool.Kind, pool.ID, pool.Revision)
}
