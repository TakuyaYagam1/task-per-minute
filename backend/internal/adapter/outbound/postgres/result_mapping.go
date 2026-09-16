package postgres

import (
	"bytes"
	"context"
	"strings"

	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

// These narrow bridges keep root-owned correction, recovery, and participant
// workflows source-compatible while the result mapping implementation lives in
// the child package.
func loadResultCommit(ctx context.Context, querier *sqlc.Queries, commit sqlc.ResultCommit) (*ResultCommitRecord, error) {
	return resultpostgres.LoadResultCommit(ctx, querier, commit)
}

func validResultScope(scope ResultScope) bool {
	return resultpostgres.ValidResultScope(scope)
}

func validResultSettlementIDs(value ResultSettlementIDs) bool {
	return resultpostgres.ValidResultSettlementIDs(value)
}

func validSeriesResultReason(reason string) bool {
	return resultpostgres.ValidSeriesResultReason(reason)
}

func resultAttemptParams(scope ResultScope) sqlc.LockResultAttemptParams {
	return resultpostgres.ResultAttemptParams(scope)
}

func submissionEventSequenceParams(scope ResultScope) sqlc.AllocateSubmissionEventSequenceParams {
	return resultpostgres.SubmissionEventSequenceParams(scope)
}

func resultEventSequenceParams(scope ResultScope) sqlc.AllocateResultEventSequenceParams {
	return resultpostgres.ResultEventSequenceParams(scope)
}

func resultLookupError(operation string, err error) error {
	return resultpostgres.ResultLookupError(operation, err)
}

func resultCASWriteError(operation string, err error) error {
	return resultpostgres.ResultCASWriteError(operation, err)
}

func optionalTrimmedString(value string) *string {
	if value == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
}

func zeroDigest(value []byte) bool {
	if len(value) != 32 {
		return true
	}
	return bytes.Equal(value, make([]byte, 32))
}
