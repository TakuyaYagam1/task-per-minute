package domain_test

import (
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestReconnectCycleLimit(t *testing.T) {
	t.Parallel()

	if domain.ReconnectCycleLimit != 2 {
		t.Fatalf("ReconnectCycleLimit = %d, want 2", domain.ReconnectCycleLimit)
	}
}
