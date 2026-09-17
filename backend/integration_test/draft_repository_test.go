//go:build integration

package integration_test

import (
	"testing"

	draftintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/draft"
)

func TestDraftRepositoryPersistsOneImmutableRevisionChain(t *testing.T) {
	draftintegration.RunDraftRepositoryPersistsOneImmutableRevisionChain(t, sharedPool)
}

func TestDraftRepositorySeparatesActorOrderFromSeriesIdentity(t *testing.T) {
	draftintegration.RunDraftRepositorySeparatesActorOrderFromSeriesIdentity(t, sharedPool)
}
