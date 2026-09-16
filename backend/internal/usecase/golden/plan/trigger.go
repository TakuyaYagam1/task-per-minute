package plan

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

const goldenTopFourCutoff = 4

var ErrInvalidStandingsProjection = errors.New("invalid Golden standings projection")

// StandingsProjection is the immutable final Swiss input used by the
// Golden topology. Payload and PayloadDigest bind the replayable standings to
// the exact projection revision identity.
type StandingsProjection struct {
	TournamentID       uuid.UUID
	ProjectionID       uuid.UUID
	RevisionID         domain.DerivedRevisionID
	RevisionNo         int
	PreviousRevisionID *domain.DerivedRevisionID
	Final              bool
	Standings          []swissusecase.NormalStanding
	Payload            []byte
	PayloadDigest      [sha256.Size]byte
}

type GroupMemberSeed struct {
	ParticipantID     uuid.UUID
	Points            int
	Buchholz          int
	HeadToHeadPoints  int
	HeadToHeadApplied bool
	EffectiveTime     time.Duration
	AcceptedSolveTime *time.Duration
	Seed              int
}

type TieGroupSeed struct {
	TournamentID                  uuid.UUID
	SourceProjectionRevisionID    domain.DerivedRevisionID
	SourceProjectionPayloadDigest [sha256.Size]byte
	PositionFrom                  int
	PositionTo                    int
	Members                       []GroupMemberSeed
}

type TieSegment struct {
	PositionFrom    int
	PositionTo      int
	Golden          bool
	NormalStandings []swissusecase.NormalStanding
	Members         []GroupMemberSeed
}

type TiePartition struct {
	TournamentID                  uuid.UUID
	SourceProjectionRevisionID    domain.DerivedRevisionID
	SourceProjectionPayloadDigest [sha256.Size]byte
	Segments                      []TieSegment
}

type goldenStandingGroupKey struct {
	points   int
	buchholz int
}

func NewStandingsProjection(
	tournamentID uuid.UUID,
	projectionID uuid.UUID,
	revisionID domain.DerivedRevisionID,
	revisionNo int,
	previousRevisionID *domain.DerivedRevisionID,
	final bool,
	standings []swissusecase.NormalStanding,
) (StandingsProjection, error) {
	projection := StandingsProjection{
		TournamentID:       tournamentID,
		ProjectionID:       projectionID,
		RevisionID:         revisionID,
		RevisionNo:         revisionNo,
		PreviousRevisionID: cloneGoldenRevisionID(previousRevisionID),
		Final:              final,
		Standings:          cloneGoldenStandings(standings),
	}
	payload, err := goldenStandingsPayload(projection)
	if err != nil {
		return StandingsProjection{}, goldenStandingsError("encode payload: %v", err)
	}
	projection.Payload = payload
	projection.PayloadDigest = sha256.Sum256(payload)
	if err := projection.Validate(); err != nil {
		return StandingsProjection{}, err
	}
	return projection.Snapshot(), nil
}

func (p StandingsProjection) Validate() error {
	if err := validateGoldenProjectionIdentity(p); err != nil {
		return err
	}
	if err := validateGoldenProjectionLineage(p); err != nil {
		return err
	}
	if !p.Final || !domain.TournamentPresetV1.ValidRosterSize(len(p.Standings)) {
		return goldenStandingsError("source is not a complete final Swiss projection")
	}
	if err := validateGoldenCanonicalStandings(p.Standings); err != nil {
		return err
	}
	return validateGoldenProjectionPayload(p)
}

func validateGoldenProjectionIdentity(p StandingsProjection) error {
	if p.TournamentID == uuid.Nil || p.ProjectionID == uuid.Nil || p.RevisionID.IsZero() ||
		p.ProjectionID == p.TournamentID || p.RevisionID.UUID() == p.ProjectionID ||
		p.RevisionID.UUID() == p.TournamentID {
		return goldenStandingsError("invalid source identity")
	}
	return nil
}

func validateGoldenProjectionLineage(p StandingsProjection) error {
	if p.RevisionNo < 1 || (p.RevisionNo == 1 && p.PreviousRevisionID != nil) ||
		(p.RevisionNo > 1 && (p.PreviousRevisionID == nil || p.PreviousRevisionID.IsZero())) ||
		(p.PreviousRevisionID != nil && (*p.PreviousRevisionID == p.RevisionID ||
			p.PreviousRevisionID.UUID() == p.TournamentID || p.PreviousRevisionID.UUID() == p.ProjectionID)) {
		return goldenStandingsError("invalid source lineage")
	}
	return nil
}

func validateGoldenProjectionPayload(p StandingsProjection) error {
	payload, err := goldenStandingsPayload(p)
	if err != nil || len(p.Payload) == 0 || !bytes.Equal(payload, p.Payload) ||
		sha256.Sum256(p.Payload) != p.PayloadDigest {
		return goldenStandingsError("source replay payload changed")
	}
	return nil
}

func (p StandingsProjection) Snapshot() StandingsProjection {
	clone := p
	clone.PreviousRevisionID = cloneGoldenRevisionID(p.PreviousRevisionID)
	clone.Standings = cloneGoldenStandings(p.Standings)
	clone.Payload = append([]byte(nil), p.Payload...)
	return clone
}

func PartitionTies(source StandingsProjection) (TiePartition, error) {
	if err := source.Validate(); err != nil {
		return TiePartition{}, err
	}
	partition := TiePartition{
		TournamentID:                  source.TournamentID,
		SourceProjectionRevisionID:    source.RevisionID,
		SourceProjectionPayloadDigest: source.PayloadDigest,
		Segments:                      make([]TieSegment, 0, len(source.Standings)),
	}
	for start := 0; start < len(source.Standings); {
		end := start + 1
		for end < len(source.Standings) && source.Standings[end].Points == source.Standings[start].Points {
			end++
		}
		positionFrom := start + 1
		positionTo := end
		impacting := end-start > 1 && positionFrom <= goldenTopFourCutoff
		if impacting {
			partition.Segments = append(partition.Segments, TieSegment{
				PositionFrom: positionFrom,
				PositionTo:   positionTo,
				Golden:       true,
				Members:      goldenMemberSeeds(source.Standings[start:end]),
			})
		} else {
			partition.Segments = append(partition.Segments, TieSegment{
				PositionFrom:    positionFrom,
				PositionTo:      positionTo,
				NormalStandings: cloneGoldenStandings(source.Standings[start:end]),
			})
		}
		start = end
	}
	return cloneGoldenTiePartition(partition), nil
}

func (p TiePartition) Groups() []TieGroupSeed {
	groups := make([]TieGroupSeed, 0, len(p.Segments))
	for _, segment := range p.Segments {
		if !segment.Golden {
			continue
		}
		groups = append(groups, TieGroupSeed{
			TournamentID:                  p.TournamentID,
			SourceProjectionRevisionID:    p.SourceProjectionRevisionID,
			SourceProjectionPayloadDigest: p.SourceProjectionPayloadDigest,
			PositionFrom:                  segment.PositionFrom,
			PositionTo:                    segment.PositionTo,
			Members:                       CloneGroupMemberSeeds(segment.Members),
		})
	}
	return groups
}

func (s TieGroupSeed) Snapshot() TieGroupSeed {
	clone := s
	clone.Members = CloneGroupMemberSeeds(s.Members)
	return clone
}

type goldenStandingsPayloadDocument struct {
	TournamentID       string                          `json:"tournament_id"`
	ProjectionID       string                          `json:"projection_id"`
	RevisionID         string                          `json:"revision_id"`
	RevisionNo         int                             `json:"revision_no"`
	PreviousRevisionID *string                         `json:"previous_revision_id,omitempty"`
	Final              bool                            `json:"final"`
	Entries            []goldenStandingPayloadDocument `json:"entries"`
}

type goldenStandingPayloadDocument struct {
	ParticipantID     string                   `json:"participant_id"`
	Position          int                      `json:"position"`
	Points            int                      `json:"points"`
	PointsLabel       swissusecase.PointsLabel `json:"points_label"`
	Buchholz          int                      `json:"buchholz"`
	HeadToHeadPoints  int                      `json:"head_to_head_points"`
	HeadToHeadApplied bool                     `json:"head_to_head_applied"`
	EffectiveTime     time.Duration            `json:"effective_time"`
	AcceptedSolveTime *time.Duration           `json:"accepted_solve_time,omitempty"`
	Seed              int                      `json:"seed"`
}

func goldenStandingsPayload(projection StandingsProjection) ([]byte, error) {
	document := goldenStandingsPayloadDocument{
		TournamentID: projection.TournamentID.String(),
		ProjectionID: projection.ProjectionID.String(),
		RevisionID:   projection.RevisionID.UUID().String(),
		RevisionNo:   projection.RevisionNo,
		Final:        projection.Final,
		Entries:      make([]goldenStandingPayloadDocument, len(projection.Standings)),
	}
	if projection.PreviousRevisionID != nil {
		value := projection.PreviousRevisionID.UUID().String()
		document.PreviousRevisionID = &value
	}
	for index, standing := range projection.Standings {
		document.Entries[index] = goldenStandingPayloadDocument{
			ParticipantID: standing.ParticipantID.String(), Position: standing.Position,
			Points: standing.Points, PointsLabel: standing.PointsLabel, Buchholz: standing.Buchholz,
			HeadToHeadPoints: standing.HeadToHeadPoints, HeadToHeadApplied: standing.HeadToHeadApplied,
			EffectiveTime:     standing.EffectiveTime,
			AcceptedSolveTime: cloneGoldenDuration(standing.AcceptedSolveTime), Seed: standing.Seed,
		}
	}
	return json.Marshal(document)
}

func validateGoldenCanonicalStandings(standings []swissusecase.NormalStanding) error {
	participants := make(map[uuid.UUID]struct{}, len(standings))
	seeds := make(map[int]struct{}, len(standings))
	for index, standing := range standings {
		if err := validateGoldenStanding(index, standing, standings, participants, seeds); err != nil {
			return err
		}
	}
	return validateGoldenHeadToHeadGroups(standings)
}

func validateGoldenStanding(
	index int,
	standing swissusecase.NormalStanding,
	standings []swissusecase.NormalStanding,
	participants map[uuid.UUID]struct{},
	seeds map[int]struct{},
) error {
	if err := validateGoldenStandingValues(index, standing); err != nil {
		return err
	}
	if !standing.HeadToHeadApplied && standing.HeadToHeadPoints != 0 {
		return goldenStandingsError("head-to-head points exist without evidence")
	}
	if _, duplicate := participants[standing.ParticipantID]; duplicate {
		return goldenStandingsError("duplicate participant")
	}
	if _, duplicate := seeds[standing.Seed]; duplicate {
		return goldenStandingsError("duplicate stable seed")
	}
	participants[standing.ParticipantID] = struct{}{}
	seeds[standing.Seed] = struct{}{}
	if index > 0 && swissusecase.StandingLess(standing, standings[index-1]) {
		return goldenStandingsError("standings are not in canonical normal order")
	}
	return nil
}

func validateGoldenStandingValues(index int, standing swissusecase.NormalStanding) error {
	if standing.ParticipantID == uuid.Nil || standing.Position != index+1 || standing.Points < 0 ||
		standing.PointsLabel != swissusecase.PointsFinal || standing.Buchholz < 0 || standing.HeadToHeadPoints < 0 ||
		standing.EffectiveTime < 0 || standing.Seed < 1 ||
		(standing.AcceptedSolveTime != nil && (*standing.AcceptedSolveTime < 0 || *standing.AcceptedSolveTime > standing.EffectiveTime)) {
		return goldenStandingsError("malformed standing at position %d", index+1)
	}
	return nil
}

func validateGoldenHeadToHeadGroups(standings []swissusecase.NormalStanding) error {
	groups := make(map[goldenStandingGroupKey][]swissusecase.NormalStanding, len(standings))
	for _, standing := range standings {
		key := goldenStandingGroupKey{points: standing.Points, buchholz: standing.Buchholz}
		groups[key] = append(groups[key], standing)
	}
	for _, group := range groups {
		applied := 0
		for _, standing := range group {
			if standing.HeadToHeadApplied {
				applied++
			}
		}
		if applied != 0 && (len(group) != 2 || applied != 2) {
			return goldenStandingsError("head-to-head evidence does not cover one exact pair")
		}
	}
	return nil
}

func goldenMemberSeeds(standings []swissusecase.NormalStanding) []GroupMemberSeed {
	members := make([]GroupMemberSeed, len(standings))
	for index, standing := range standings {
		members[index] = GroupMemberSeed{
			ParticipantID: standing.ParticipantID, Points: standing.Points, Buchholz: standing.Buchholz,
			HeadToHeadPoints: standing.HeadToHeadPoints, HeadToHeadApplied: standing.HeadToHeadApplied,
			EffectiveTime:     standing.EffectiveTime,
			AcceptedSolveTime: cloneGoldenDuration(standing.AcceptedSolveTime), Seed: standing.Seed,
		}
	}
	sort.Slice(members, func(i, j int) bool {
		return bytes.Compare(members[i].ParticipantID[:], members[j].ParticipantID[:]) < 0
	})
	return members
}

func cloneGoldenStandings(input []swissusecase.NormalStanding) []swissusecase.NormalStanding {
	result := append([]swissusecase.NormalStanding(nil), input...)
	for index := range result {
		result[index].AcceptedSolveTime = cloneGoldenDuration(input[index].AcceptedSolveTime)
	}
	return result
}

// CloneGroupMemberSeeds returns a deep copy of immutable group seed data.
func CloneGroupMemberSeeds(input []GroupMemberSeed) []GroupMemberSeed {
	result := append([]GroupMemberSeed(nil), input...)
	for index := range result {
		result[index].AcceptedSolveTime = cloneGoldenDuration(input[index].AcceptedSolveTime)
	}
	return result
}

func cloneGoldenTiePartition(input TiePartition) TiePartition {
	result := input
	result.Segments = append([]TieSegment(nil), input.Segments...)
	for index := range result.Segments {
		result.Segments[index].NormalStandings = cloneGoldenStandings(input.Segments[index].NormalStandings)
		result.Segments[index].Members = CloneGroupMemberSeeds(input.Segments[index].Members)
	}
	return result
}

func cloneGoldenRevisionID(value *domain.DerivedRevisionID) *domain.DerivedRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGoldenDuration(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func goldenStandingsError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidStandingsProjection, fmt.Sprintf(format, arguments...))
}
