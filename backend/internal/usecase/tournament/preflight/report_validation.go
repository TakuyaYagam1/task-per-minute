package preflight

import (
	"sort"
	"strings"
	"unicode"
)

func validPreflightSourceRevisions(revisions []SourceRevision) bool {
	if len(revisions) == 0 {
		return false
	}
	for i, revision := range revisions {
		if !operatorSafeString(revision.Source) || !operatorSafeString(revision.Value) {
			return false
		}
		if i > 0 && !preflightSourceRevisionLess(revisions[i-1], revision) {
			return false
		}
	}
	return true
}

func validPreflightChecks(checks []Check) bool {
	if len(checks) != len(preflightReportCheckCodes) {
		return false
	}
	for i, check := range checks {
		if check.Code != preflightReportCheckCodes[i] || !operatorSafeString(string(check.Code)) ||
			!operatorSafeString(check.Explanation) || !sortedUniqueOperatorStrings(check.Evidence, true) {
			return false
		}
		if check.Passed && len(check.Evidence) == 0 {
			return false
		}
	}
	return true
}

func sortedUniqueOperatorStrings(values []string, allowEmpty bool) bool {
	if len(values) == 0 {
		return allowEmpty
	}
	for i, value := range values {
		if !operatorSafeString(value) || i > 0 && values[i-1] >= value {
			return false
		}
	}
	return true
}

func operatorSafeString(value string) bool {
	return value != "" && strings.IndexFunc(value, unicode.IsControl) == -1
}

func appendClonedPreflightChecks(out []Check, checks []Check) []Check {
	for _, check := range checks {
		check.Evidence = append([]string{}, check.Evidence...)
		out = append(out, check)
	}
	return out
}

func uniquePreflightSourceRevisions(revisions []SourceRevision) []SourceRevision {
	result := revisions[:0]
	for _, revision := range revisions {
		if len(result) == 0 || result[len(result)-1] != revision {
			result = append(result, revision)
		}
	}
	return result
}

func preflightSourceRevisionLess(first, second SourceRevision) bool {
	return first.Source < second.Source || first.Source == second.Source && first.Value < second.Value
}

func sortedUniqueStrings(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
