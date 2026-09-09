package resultprojection

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	DependencyArtifact       = "artifact"
	DependencyOfficialResult = "official_result"
	DependencyGoldenPosition = "golden_position"
)

type FinalScope struct {
	TournamentID  uuid.UUID
	RosterID      uuid.UUID
	SeriesID      uuid.UUID
	GameAttemptID uuid.UUID
}

type FinalHeadExpectation struct {
	TournamentRevision     int64
	ProjectionRevision     int64
	GameAttemptRevision    int64
	GameResultRevisionID   domain.OfficialResultRevisionID
	SeriesRevision         int64
	ScoreHeadRevision      int64
	ScoreRevisionID        domain.SeriesScoreRevisionID
	SeriesResultRevisionID domain.OfficialResultRevisionID
	WinnerID               uuid.UUID
}

type PublicationIDs struct {
	RevisionID uuid.UUID
	CutoffID   uuid.UUID
}

type PublicationMember struct {
	ParticipantID uuid.UUID
	Position      int32
	ScoreMilli    *int64
}

type PublicationDependency struct {
	ID                       uuid.UUID
	Kind                     string
	DependsOnArtifactID      *uuid.UUID
	OfficialResultRevisionID *uuid.UUID
	OfficialResultSeriesID   *uuid.UUID
	GoldenPositionCommitID   *uuid.UUID
}

type PublicationArtifact struct {
	ID            uuid.UUID
	Kind          domain.ArtifactKind
	Key           string
	Payload       json.RawMessage
	PayloadDigest [sha256.Size]byte
	Members       []PublicationMember
	Dependencies  []PublicationDependency
}

// FinalPublication is the immutable usecase contract for publishing the
// terminal playoff projection. The repository must recheck every expected head
// under database row locks and commit all writes in one transaction.
type FinalPublication struct {
	IDs                    PublicationIDs
	Scope                  FinalScope
	Expected               FinalHeadExpectation
	Artifacts              []PublicationArtifact
	ResultProjectionDigest [sha256.Size]byte
	Reason                 string
	SupersessionReason     string
	CutoffAt               time.Time
	CreatedAt              time.Time
	PublishedAt            time.Time
}

type FinalPublicationReceipt struct {
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	TournamentRevision   int64
	OutboxEventID        uuid.UUID
	OutboxOrdinal        int16
	Changed              bool
}

// FinalRevisionConflictError carries the current locked revisions returned by the
// persistence boundary. Callers do not need a second, racy read after a CAS
// failure.
type FinalRevisionConflictError struct {
	TournamentID                   uuid.UUID
	ExpectedTournamentRevision     int64
	CurrentTournamentRevision      int64
	CurrentTournamentState         domain.TournamentState
	ExpectedProjectionRevision     int64
	CurrentProjectionRevision      int64
	CurrentProjectionRevisionID    uuid.UUID
	ExpectedGameAttemptRevision    int64
	CurrentGameAttemptRevision     int64
	ExpectedGameResultRevisionID   domain.OfficialResultRevisionID
	CurrentGameResultRevisionID    domain.OfficialResultRevisionID
	CurrentGameResultRevision      int64
	ExpectedSeriesRevision         int64
	CurrentSeriesRevision          int64
	ExpectedScoreHeadRevision      int64
	CurrentScoreHeadRevision       int64
	ExpectedScoreRevisionID        domain.SeriesScoreRevisionID
	CurrentScoreRevisionID         domain.SeriesScoreRevisionID
	ExpectedSeriesResultRevisionID domain.OfficialResultRevisionID
	CurrentSeriesResultRevisionID  domain.OfficialResultRevisionID
}

func (e *FinalRevisionConflictError) Error() string {
	return "final projection revision conflict"
}

func (e *FinalRevisionConflictError) Unwrap() error {
	return domain.ErrConflict
}

type FinalPublicationRepository interface {
	PublishFinal(
		ctx context.Context,
		publication FinalPublication,
	) (FinalPublicationReceipt, error)
}

func (p FinalPublication) Validate() error {
	if !validFinalScope(p.Scope) || !validFinalExpectation(p.Expected) ||
		p.IDs.RevisionID == uuid.Nil || p.IDs.CutoffID == uuid.Nil ||
		p.IDs.RevisionID == p.IDs.CutoffID || zeroPublicationDigest(p.ResultProjectionDigest) ||
		!validPublicationText(p.Reason) || !validPublicationText(p.SupersessionReason) ||
		!domain.IsValidServerTime(p.CutoffAt) || !domain.IsValidServerTime(p.CreatedAt) ||
		!domain.IsValidServerTime(p.PublishedAt) || p.CutoffAt.After(p.CreatedAt) ||
		p.CreatedAt.After(p.PublishedAt) {
		return domain.ErrValidation
	}
	if !validFinalArtifactSet(p.Artifacts, p.Scope, p.Expected) {
		return domain.ErrValidation
	}
	return nil
}

func (p FinalPublication) Snapshot() FinalPublication {
	clone := p
	clone.Artifacts = make([]PublicationArtifact, len(p.Artifacts))
	for index, artifact := range p.Artifacts {
		clone.Artifacts[index] = artifact
		clone.Artifacts[index].Payload = append([]byte(nil), artifact.Payload...)
		clone.Artifacts[index].Members = make([]PublicationMember, len(artifact.Members))
		for memberIndex, member := range artifact.Members {
			clone.Artifacts[index].Members[memberIndex] = member
			if member.ScoreMilli != nil {
				score := *member.ScoreMilli
				clone.Artifacts[index].Members[memberIndex].ScoreMilli = &score
			}
		}
		clone.Artifacts[index].Dependencies = make([]PublicationDependency, len(artifact.Dependencies))
		for dependencyIndex, dependency := range artifact.Dependencies {
			clone.Artifacts[index].Dependencies[dependencyIndex] = clonePublicationDependency(dependency)
		}
	}
	return clone
}

func validFinalScope(scope FinalScope) bool {
	ids := [4]uuid.UUID{scope.TournamentID, scope.RosterID, scope.SeriesID, scope.GameAttemptID}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func validFinalExpectation(expected FinalHeadExpectation) bool {
	return expected.TournamentRevision > 0 && expected.ProjectionRevision >= 0 &&
		expected.GameAttemptRevision > 0 && !expected.GameResultRevisionID.IsZero() &&
		expected.SeriesRevision > 0 && expected.ScoreHeadRevision > 0 &&
		!expected.ScoreRevisionID.IsZero() && !expected.SeriesResultRevisionID.IsZero() &&
		expected.WinnerID != uuid.Nil
}

func validFinalArtifactSet(
	artifacts []PublicationArtifact,
	scope FinalScope,
	expected FinalHeadExpectation,
) bool {
	if len(artifacts) != 4 {
		return false
	}
	wanted := map[domain.ArtifactKind]bool{
		domain.ArtifactKindStandings: false,
		domain.ArtifactKindBracket:   false,
		domain.ArtifactKindTopFour:   false,
		domain.ArtifactKindChampion:  false,
	}
	ids := make(map[uuid.UUID]struct{}, len(artifacts))
	var bracketID uuid.UUID
	var champion PublicationArtifact
	for _, artifact := range artifacts {
		if _, exists := wanted[artifact.Kind]; !exists || wanted[artifact.Kind] ||
			!validPublicationArtifact(artifact) {
			return false
		}
		if _, duplicate := ids[artifact.ID]; duplicate {
			return false
		}
		ids[artifact.ID] = struct{}{}
		wanted[artifact.Kind] = true
		if artifact.Kind == domain.ArtifactKindBracket {
			bracketID = artifact.ID
		}
		if artifact.Kind == domain.ArtifactKindChampion {
			champion = artifact
		}
	}
	return validChampionEvidence(champion, bracketID, scope, expected)
}

func validPublicationArtifact(artifact PublicationArtifact) bool {
	if artifact.ID == uuid.Nil || !validPublicationText(artifact.Key) || len(artifact.Key) > 128 ||
		!json.Valid(artifact.Payload) || zeroPublicationDigest(artifact.PayloadDigest) ||
		sha256.Sum256(artifact.Payload) != artifact.PayloadDigest || len(artifact.Members) == 0 ||
		len(artifact.Dependencies) == 0 {
		return false
	}
	if !validPublicationMembers(artifact) || !validPublicationPayload(artifact) {
		return false
	}
	for _, dependency := range artifact.Dependencies {
		if !validPublicationDependency(dependency) {
			return false
		}
	}
	return true
}

func validPublicationMembers(artifact PublicationArtifact) bool {
	participants := make(map[uuid.UUID]struct{}, len(artifact.Members))
	positions := make(map[int32]struct{}, len(artifact.Members))
	for _, member := range artifact.Members {
		if member.ParticipantID == uuid.Nil || member.Position < 1 {
			return false
		}
		if _, duplicate := participants[member.ParticipantID]; duplicate {
			return false
		}
		if _, duplicate := positions[member.Position]; duplicate {
			return false
		}
		participants[member.ParticipantID] = struct{}{}
		positions[member.Position] = struct{}{}
		if artifact.Kind == domain.ArtifactKindStandings {
			if member.ScoreMilli == nil || *member.ScoreMilli < 0 {
				return false
			}
		} else if member.ScoreMilli != nil {
			return false
		}
	}
	return true
}

func validPublicationPayload(artifact PublicationArtifact) bool {
	var payload map[string]json.RawMessage
	if json.Unmarshal(artifact.Payload, &payload) != nil {
		return false
	}
	switch artifact.Kind {
	case domain.ArtifactKindStandings:
		_, legacy := payload["standings"]
		return !legacy && nonEmptyPublicationArray(payload["entries"])
	case domain.ArtifactKindBracket:
		_, bracketAlias := payload["bracket"]
		_, semifinalAlias := payload["semifinals"]
		return !bracketAlias && !semifinalAlias && nonEmptyPublicationArray(payload["rounds"])
	case domain.ArtifactKindTopFour:
		return len(artifact.Members) == 4 && exactPublicationArray(payload["participants"], 4)
	case domain.ArtifactKindChampion:
		participant, ok := payload["participant_id"]
		return len(artifact.Members) == 1 && ok && json.Valid(participant)
	case domain.ArtifactKindGameResult, domain.ArtifactKindSeriesScore,
		domain.ArtifactKindSeriesResult, domain.ArtifactKindGoldenGroup:
		return false
	}
	return false
}

func nonEmptyPublicationArray(raw json.RawMessage) bool {
	var values []json.RawMessage
	return json.Unmarshal(raw, &values) == nil && len(values) > 0
}

func exactPublicationArray(raw json.RawMessage, size int) bool {
	var values []json.RawMessage
	return json.Unmarshal(raw, &values) == nil && len(values) == size
}

func validPublicationDependency(dependency PublicationDependency) bool {
	if dependency.ID == uuid.Nil {
		return false
	}
	artifact := nonNilPublicationUUID(dependency.DependsOnArtifactID)
	result := nonNilPublicationUUID(dependency.OfficialResultRevisionID)
	series := nonNilPublicationUUID(dependency.OfficialResultSeriesID)
	golden := nonNilPublicationUUID(dependency.GoldenPositionCommitID)
	switch dependency.Kind {
	case DependencyArtifact:
		return artifact && !result && !series && !golden
	case DependencyOfficialResult:
		return !artifact && result && series && !golden
	case DependencyGoldenPosition:
		return !artifact && !result && !series && golden
	default:
		return false
	}
}

func validChampionEvidence(
	champion PublicationArtifact,
	bracketID uuid.UUID,
	scope FinalScope,
	expected FinalHeadExpectation,
) bool {
	if !allPublicationChecks(
		champion.Kind == domain.ArtifactKindChampion,
		bracketID != uuid.Nil,
		len(champion.Members) == 1,
	) {
		return false
	}
	if champion.Members[0].ParticipantID != expected.WinnerID {
		return false
	}
	var payload struct {
		ParticipantID uuid.UUID `json:"participant_id"`
	}
	if json.Unmarshal(champion.Payload, &payload) != nil || payload.ParticipantID != expected.WinnerID {
		return false
	}
	seriesResultID := expected.SeriesResultRevisionID.UUID()
	return championHasArtifactDependency(champion.Dependencies, bracketID) &&
		championHasResultDependency(champion.Dependencies, seriesResultID, scope.SeriesID)
}

func championHasArtifactDependency(dependencies []PublicationDependency, artifactID uuid.UUID) bool {
	for _, dependency := range dependencies {
		if allPublicationChecks(
			dependency.Kind == DependencyArtifact,
			dependency.DependsOnArtifactID != nil,
		) && *dependency.DependsOnArtifactID == artifactID {
			return true
		}
	}
	return false
}

func championHasResultDependency(
	dependencies []PublicationDependency,
	revisionID uuid.UUID,
	seriesID uuid.UUID,
) bool {
	for _, dependency := range dependencies {
		if allPublicationChecks(
			dependency.Kind == DependencyOfficialResult,
			dependency.OfficialResultRevisionID != nil,
			dependency.OfficialResultSeriesID != nil,
		) && allPublicationChecks(
			*dependency.OfficialResultRevisionID == revisionID,
			*dependency.OfficialResultSeriesID == seriesID,
		) {
			return true
		}
	}
	return false
}

func allPublicationChecks(checks ...bool) bool {
	for _, check := range checks {
		if !check {
			return false
		}
	}
	return true
}

func clonePublicationDependency(dependency PublicationDependency) PublicationDependency {
	clone := dependency
	clone.DependsOnArtifactID = clonePublicationUUID(dependency.DependsOnArtifactID)
	clone.OfficialResultRevisionID = clonePublicationUUID(dependency.OfficialResultRevisionID)
	clone.OfficialResultSeriesID = clonePublicationUUID(dependency.OfficialResultSeriesID)
	clone.GoldenPositionCommitID = clonePublicationUUID(dependency.GoldenPositionCommitID)
	return clone
}

func clonePublicationUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func nonNilPublicationUUID(value *uuid.UUID) bool {
	return value != nil && *value != uuid.Nil
}

func zeroPublicationDigest(value [sha256.Size]byte) bool {
	return value == ([sha256.Size]byte{})
}

func validPublicationText(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}
