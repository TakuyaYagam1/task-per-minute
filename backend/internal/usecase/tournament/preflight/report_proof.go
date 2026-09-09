package preflight

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"

	"github.com/google/uuid"
)

func preflightSourceRevisions(in ReportInput) []SourceRevision {
	result := []SourceRevision{
		{Source: "content", Value: strconv.FormatInt(in.Runtime.ContentRevision, 10)},
		{Source: "pairing", Value: strconv.FormatInt(in.PairingRevision, 10)},
		{Source: "roster", Value: strconv.FormatInt(in.RosterRevision, 10)},
		{Source: "runtime.configuration", Value: in.Runtime.Configuration.Revision},
		{Source: "runtime.authoritative_storage", Value: in.Runtime.Health.AuthoritativeStorage.Revision},
		{Source: "runtime.submission", Value: in.Runtime.Health.Submission.Revision},
		{Source: "runtime.task_delivery", Value: in.Runtime.Health.TaskDelivery.Revision},
		{Source: "runtime.realtime", Value: in.Runtime.Health.Realtime.Revision},
		{Source: "tasks.normal", Value: revisionIdentity(in.TaskHealth.NormalPool.ID, in.TaskHealth.NormalPool.Revision)},
		{Source: "tasks.golden", Value: revisionIdentity(in.TaskHealth.GoldenPool.ID, in.TaskHealth.GoldenPool.Revision)},
	}
	if in.Runtime.Capacity == nil {
		result = append(result, SourceRevision{Source: "capacity", Value: "missing"})
	} else {
		result = append(result, SourceRevision{Source: "capacity", Value: in.Runtime.Capacity.ID.String()})
	}
	for _, pool := range in.Structural.CategoryPools {
		result = append(result, SourceRevision{
			Source: "categories." + string(pool.Format),
			Value:  revisionIdentity(pool.ID, pool.Revision),
		})
	}
	for _, dependency := range in.Runtime.Dependencies {
		result = append(result, SourceRevision{
			Source: "dependency." + string(dependency.Name),
			Value:  dependency.Revision,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Value < result[j].Value
	})
	return uniquePreflightSourceRevisions(result)
}

func revisionIdentity(id uuid.UUID, revision int64) string {
	return id.String() + "@" + strconv.FormatInt(revision, 10)
}

func preflightProofHash(document preflightProofDocument) (string, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", preflightReportError("cannot encode proof document")
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
