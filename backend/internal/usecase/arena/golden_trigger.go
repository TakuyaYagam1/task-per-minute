package arena

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
)

const goldenTopFourCutoff = 4

var ErrInvalidGoldenStandingsProjection = errors.New("invalid Golden standings projection")

// GoldenStandingsProjection is the immutable final Swiss input used by the
// Golden topology. Payload and PayloadDigest bind the replayable standings to
// the exact projection revision identity.
type GoldenStandingsProjection struct {
	TournamentID       uuid.UUID
	ProjectionID       uuid.UUID
	RevisionID         domain.ArenaDerivedRevisionID
	RevisionNo         int
	PreviousRevisionID *domain.ArenaDerivedRevisionID
	Final              bool
	Standings          []SwissNormalStanding
	Payload            []byte
	PayloadDigest      [sha256.Size]byte
}

type GoldenGroupMemberSeed struct {
	ParticipantID     uuid.UUID
	Points            int
	Buchholz          int
	HeadToHeadPoints  int
	HeadToHeadApplied bool
	EffectiveTime     time.Duration
	AcceptedSolveTime *time.Duration
	Seed              int
}

type GoldenTieGroupSeed struct {
	TournamentID                  uuid.UUID
	SourceProjectionRevisionID    domain.ArenaDerivedRevisionID
	SourceProjectionPayloadDigest [sha256.Size]byte
	PositionFrom                  int
	PositionTo                    int
	Members                       []GoldenGroupMemberSeed
}

type GoldenTieSegment struct {
	PositionFrom    int
	PositionTo      int
	Golden          bool
	NormalStandings []SwissNormalStanding
	Members         []GoldenGroupMemberSeed
}

type GoldenTiePartition struct {
	TournamentID                  uuid.UUID
	SourceProjectionRevisionID    domain.ArenaDerivedRevisionID
	SourceProjectionPayloadDigest [sha256.Size]byte
	Segments                      []GoldenTieSegment
}

func NewGoldenStandingsProjection(
	tournamentID uuid.UUID,
	projectionID uuid.UUID,
	revisionID domain.ArenaDerivedRevisionID,
	revisionNo int,
	previousRevisionID *domain.ArenaDerivedRevisionID,
	final bool,
	standings []SwissNormalStanding,
) (GoldenStandingsProjection, error) {
	projection := GoldenStandingsProjection{
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
		return GoldenStandingsProjection{}, goldenStandingsError("encode payload: %v", err)
	}
	projection.Payload = payload
	projection.PayloadDigest = sha256.Sum256(payload)
	if err := projection.Validate(); err != nil {
		return GoldenStandingsProjection{}, err
	}
	return projection.Snapshot(), nil
}

func (p GoldenStandingsProjection) Validate() error {
	if err := validateGoldenProjectionIdentity(p); err != nil {
		return err
	}
	if err := validateGoldenProjectionLineage(p); err != nil {
		return err
	}
	if !p.Final || !domain.ArenaPresetV1.ValidRosterSize(len(p.Standings)) {
		return goldenStandingsError("source is not a complete final Swiss projection")
	}
	if err := validateGoldenCanonicalStandings(p.Standings); err != nil {
		return err
	}
	return validateGoldenProjectionPayload(p)
}

func validateGoldenProjectionIdentity(p GoldenStandingsProjection) error {
	if p.TournamentID == uuid.Nil || p.ProjectionID == uuid.Nil || p.RevisionID.IsZero() ||
		p.ProjectionID == p.TournamentID || p.RevisionID.UUID() == p.ProjectionID ||
		p.RevisionID.UUID() == p.TournamentID {
		return goldenStandingsError("invalid source identity")
	}
	return nil
}

func validateGoldenProjectionLineage(p GoldenStandingsProjection) error {
	if p.RevisionNo < 1 || (p.RevisionNo == 1 && p.PreviousRevisionID != nil) ||
		(p.RevisionNo > 1 && (p.PreviousRevisionID == nil || p.PreviousRevisionID.IsZero())) ||
		(p.PreviousRevisionID != nil && (*p.PreviousRevisionID == p.RevisionID ||
			p.PreviousRevisionID.UUID() == p.TournamentID || p.PreviousRevisionID.UUID() == p.ProjectionID)) {
		return goldenStandingsError("invalid source lineage")
	}
	return nil
}

func validateGoldenProjectionPayload(p GoldenStandingsProjection) error {
	payload, err := goldenStandingsPayload(p)
	if err != nil || len(p.Payload) == 0 || !bytes.Equal(payload, p.Payload) ||
		sha256.Sum256(p.Payload) != p.PayloadDigest {
		return goldenStandingsError("source replay payload changed")
	}
	return nil
}

func (p GoldenStandingsProjection) Snapshot() GoldenStandingsProjection {
	clone := p
	clone.PreviousRevisionID = cloneGoldenRevisionID(p.PreviousRevisionID)
	clone.Standings = cloneGoldenStandings(p.Standings)
	clone.Payload = append([]byte(nil), p.Payload...)
	return clone
}

func PartitionGoldenTies(source GoldenStandingsProjection) (GoldenTiePartition, error) {
	if err := source.Validate(); err != nil {
		return GoldenTiePartition{}, err
	}
	partition := GoldenTiePartition{
		TournamentID:                  source.TournamentID,
		SourceProjectionRevisionID:    source.RevisionID,
		SourceProjectionPayloadDigest: source.PayloadDigest,
		Segments:                      make([]GoldenTieSegment, 0, len(source.Standings)),
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
			partition.Segments = append(partition.Segments, GoldenTieSegment{
				PositionFrom: positionFrom,
				PositionTo:   positionTo,
				Golden:       true,
				Members:      goldenMemberSeeds(source.Standings[start:end]),
			})
		} else {
			partition.Segments = append(partition.Segments, GoldenTieSegment{
				PositionFrom:    positionFrom,
				PositionTo:      positionTo,
				NormalStandings: cloneGoldenStandings(source.Standings[start:end]),
			})
		}
		start = end
	}
	return cloneGoldenTiePartition(partition), nil
}

func (p GoldenTiePartition) GoldenGroups() []GoldenTieGroupSeed {
	groups := make([]GoldenTieGroupSeed, 0, len(p.Segments))
	for _, segment := range p.Segments {
		if !segment.Golden {
			continue
		}
		groups = append(groups, GoldenTieGroupSeed{
			TournamentID:                  p.TournamentID,
			SourceProjectionRevisionID:    p.SourceProjectionRevisionID,
			SourceProjectionPayloadDigest: p.SourceProjectionPayloadDigest,
			PositionFrom:                  segment.PositionFrom,
			PositionTo:                    segment.PositionTo,
			Members:                       cloneGoldenMemberSeeds(segment.Members),
		})
	}
	return groups
}

func (s GoldenTieGroupSeed) Snapshot() GoldenTieGroupSeed {
	clone := s
	clone.Members = cloneGoldenMemberSeeds(s.Members)
	return clone
}

type goldenStandingsPayloadDocument struct {
	TournamentID       string                          `json:"tournament_id"`
	ProjectionID       string                          `json:"projection_id"`
	RevisionID         string                          `json:"revision_id"`
	RevisionNo         int                             `json:"revision_no"`
	PreviousRevisionID *string                         `json:"previous_revision_id,omitempty"`
	Final              bool                            `json:"final"`
	Standings          []goldenStandingPayloadDocument `json:"standings"`
}

type goldenStandingPayloadDocument struct {
	ParticipantID     string           `json:"participant_id"`
	Position          int              `json:"position"`
	Points            int              `json:"points"`
	PointsLabel       SwissPointsLabel `json:"points_label"`
	Buchholz          int              `json:"buchholz"`
	HeadToHeadPoints  int              `json:"head_to_head_points"`
	HeadToHeadApplied bool             `json:"head_to_head_applied"`
	EffectiveTime     time.Duration    `json:"effective_time"`
	AcceptedSolveTime *time.Duration   `json:"accepted_solve_time,omitempty"`
	Seed              int              `json:"seed"`
}

func goldenStandingsPayload(projection GoldenStandingsProjection) ([]byte, error) {
	document := goldenStandingsPayloadDocument{
		TournamentID: projection.TournamentID.String(),
		ProjectionID: projection.ProjectionID.String(),
		RevisionID:   projection.RevisionID.UUID().String(),
		RevisionNo:   projection.RevisionNo,
		Final:        projection.Final,
		Standings:    make([]goldenStandingPayloadDocument, len(projection.Standings)),
	}
	if projection.PreviousRevisionID != nil {
		value := projection.PreviousRevisionID.UUID().String()
		document.PreviousRevisionID = &value
	}
	for index, standing := range projection.Standings {
		document.Standings[index] = goldenStandingPayloadDocument{
			ParticipantID: standing.ParticipantID.String(), Position: standing.Position,
			Points: standing.Points, PointsLabel: standing.PointsLabel, Buchholz: standing.Buchholz,
			HeadToHeadPoints: standing.HeadToHeadPoints, HeadToHeadApplied: standing.HeadToHeadApplied,
			EffectiveTime:     standing.EffectiveTime,
			AcceptedSolveTime: cloneGoldenDuration(standing.AcceptedSolveTime), Seed: standing.Seed,
		}
	}
	return json.Marshal(document)
}

func validateGoldenCanonicalStandings(standings []SwissNormalStanding) error {
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
	standing SwissNormalStanding,
	standings []SwissNormalStanding,
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
	if index > 0 && swissStandingLess(standing, standings[index-1]) {
		return goldenStandingsError("standings are not in canonical normal order")
	}
	return nil
}

func validateGoldenStandingValues(index int, standing SwissNormalStanding) error {
	if standing.ParticipantID == uuid.Nil || standing.Position != index+1 || standing.Points < 0 ||
		standing.PointsLabel != SwissPointsFinal || standing.Buchholz < 0 || standing.HeadToHeadPoints < 0 ||
		standing.EffectiveTime < 0 || standing.Seed < 1 ||
		(standing.AcceptedSolveTime != nil && (*standing.AcceptedSolveTime < 0 || *standing.AcceptedSolveTime > standing.EffectiveTime)) {
		return goldenStandingsError("malformed standing at position %d", index+1)
	}
	return nil
}

func validateGoldenHeadToHeadGroups(standings []SwissNormalStanding) error {
	groups := make(map[swissStandingGroupKey][]SwissNormalStanding, len(standings))
	for _, standing := range standings {
		key := swissStandingGroupKey{points: standing.Points, buchholz: standing.Buchholz}
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

func goldenMemberSeeds(standings []SwissNormalStanding) []GoldenGroupMemberSeed {
	members := make([]GoldenGroupMemberSeed, len(standings))
	for index, standing := range standings {
		members[index] = GoldenGroupMemberSeed{
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

func cloneGoldenStandings(input []SwissNormalStanding) []SwissNormalStanding {
	result := append([]SwissNormalStanding(nil), input...)
	for index := range result {
		result[index].AcceptedSolveTime = cloneGoldenDuration(input[index].AcceptedSolveTime)
	}
	return result
}

func cloneGoldenMemberSeeds(input []GoldenGroupMemberSeed) []GoldenGroupMemberSeed {
	result := append([]GoldenGroupMemberSeed(nil), input...)
	for index := range result {
		result[index].AcceptedSolveTime = cloneGoldenDuration(input[index].AcceptedSolveTime)
	}
	return result
}

func cloneGoldenTiePartition(input GoldenTiePartition) GoldenTiePartition {
	result := input
	result.Segments = append([]GoldenTieSegment(nil), input.Segments...)
	for index := range result.Segments {
		result.Segments[index].NormalStandings = cloneGoldenStandings(input.Segments[index].NormalStandings)
		result.Segments[index].Members = cloneGoldenMemberSeeds(input.Segments[index].Members)
	}
	return result
}

func cloneGoldenRevisionID(value *domain.ArenaDerivedRevisionID) *domain.ArenaDerivedRevisionID {
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
	return fmt.Errorf("%w: %s", ErrInvalidGoldenStandingsProjection, fmt.Sprintf(format, arguments...))
}
