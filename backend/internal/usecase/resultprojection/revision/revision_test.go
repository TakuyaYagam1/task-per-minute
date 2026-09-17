package revision_test

import (
	"errors"
	"testing"

	revisionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func TestRevisionCapabilityRejectsEmptyDAG(t *testing.T) {
	_, err := revisionusecase.BuildRevisionDAG(revisionusecase.RevisionDAGInput{})
	if !errors.Is(err, revisionusecase.ErrInvalidRevisionDAG) {
		t.Fatalf("BuildRevisionDAG() error = %v", err)
	}
}

func TestRevisionCapabilityRejectsInvalidProjectionSource(t *testing.T) {
	_, err := revisionusecase.ProjectOfficialResult(revisionusecase.OfficialResultProjectionInput{
		TerminalSource: revisionusecase.TerminalResultSourcePlayed,
	})
	if !errors.Is(err, revisionusecase.ErrInvalidOfficialResultProjection) {
		t.Fatalf("ProjectOfficialResult() error = %v", err)
	}
}
