package canonical

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

var ErrInvalidCanonicalMaterialization = errors.New("invalid canonical projection materialization")

// CanonicalSwissParticipant is a normalized roster identity. StableSeed comes
// from the locked participant record, never from a correction request.
type CanonicalSwissParticipant struct {
	ID         uuid.UUID
	StableSeed int
}

// CanonicalBracketMatch is the locked normalized bracket state. It is supplied
// only when the bracket already exists in the current authoritative stage.
type CanonicalBracketMatch struct {
	Position            int
	SeriesID            uuid.UUID
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	State               domain.SeriesState
	FirstWins           int
	SecondWins          int
}

// CanonicalTopFourPosition is the current normalized qualification position.
// Without a GoldenPositionCommitID it must equal the canonical Swiss position.
// A non-zero commit ID is the only authority that may replace that participant.
type CanonicalTopFourPosition struct {
	ParticipantID          uuid.UUID
	Position               int
	GoldenPositionCommitID *uuid.UUID
}

// CanonicalMaterializationInput is a server-owned projection authority. Rounds
// are built from immutable Swiss ledger rows and may contain only completed
// official rounds. TopFour and Bracket are explicit normalized stage state;
// their presence proves that a later stage already exists.
type CanonicalMaterializationInput struct {
	TournamentID  uuid.UUID
	Participants  []CanonicalSwissParticipant
	SwissLedger   []CanonicalSwissPointLedgerEntry
	SwissComplete bool
	TopFour       []CanonicalTopFourPosition
	Bracket       []CanonicalBracketMatch
	ArtifactKinds []domain.ArtifactKind
}

type CanonicalMaterializedMember struct {
	ParticipantID uuid.UUID
	Position      int
	ScoreMilli    *int64
}

type CanonicalMaterializedArtifact struct {
	Kind                    domain.ArtifactKind
	Payload                 []byte
	PayloadDigest           [sha256.Size]byte
	Members                 []CanonicalMaterializedMember
	GoldenPositionCommitIDs []uuid.UUID
}

type CanonicalMaterialization struct {
	Artifacts []CanonicalMaterializedArtifact
}

func BuildCanonicalMaterialization(
	input CanonicalMaterializationInput,
) (CanonicalMaterialization, error) {
	requested, err := canonicalMaterializationKinds(input.ArtifactKinds)
	if err != nil || input.TournamentID == uuid.Nil {
		return CanonicalMaterialization{}, ErrInvalidCanonicalMaterialization
	}
	participants, seeds, err := canonicalSwissParticipants(input.Participants)
	if err != nil || len(input.SwissLedger) == 0 {
		return CanonicalMaterialization{}, ErrInvalidCanonicalMaterialization
	}
	rounds, err := BuildCanonicalSwissRounds(input.SwissLedger)
	if err != nil {
		return CanonicalMaterialization{}, ErrInvalidCanonicalMaterialization
	}
	if err := canonicalSwissLedgerSeeds(input.Participants, input.SwissLedger); err != nil {
		return CanonicalMaterialization{}, ErrInvalidCanonicalMaterialization
	}
	standings, err := swissusecase.DeriveRoundStandings(participants, seeds, rounds, input.SwissComplete)
	if err != nil {
		return CanonicalMaterialization{}, ErrInvalidCanonicalMaterialization
	}
	artifacts := make([]CanonicalMaterializedArtifact, 0, len(requested))
	standingsArtifact, err := canonicalStandingsArtifact(standings)
	if err != nil {
		return CanonicalMaterialization{}, err
	}
	for _, kind := range requested {
		//nolint:exhaustive // This switch intentionally handles only the valid states for this boundary.
		switch kind {
		case domain.ArtifactKindStandings:
			artifacts = append(artifacts, standingsArtifact)
		case domain.ArtifactKindTopFour:
			artifact, artifactErr := canonicalTopFourArtifact(input, standings)
			if artifactErr != nil {
				return CanonicalMaterialization{}, artifactErr
			}
			artifacts = append(artifacts, artifact)
		case domain.ArtifactKindBracket:
			artifact, artifactErr := canonicalBracketArtifact(input)
			if artifactErr != nil {
				return CanonicalMaterialization{}, artifactErr
			}
			artifacts = append(artifacts, artifact)
		default:
			return CanonicalMaterialization{}, ErrInvalidCanonicalMaterialization
		}
	}
	return CanonicalMaterialization{Artifacts: artifacts}, nil
}

func canonicalSwissLedgerSeeds(
	participants []CanonicalSwissParticipant,
	entries []CanonicalSwissPointLedgerEntry,
) error {
	seeds := make(map[uuid.UUID]int, len(participants))
	for _, participant := range participants {
		seeds[participant.ID] = participant.StableSeed
	}
	for _, entry := range entries {
		if seed, exists := seeds[entry.ParticipantID]; !exists || seed != entry.StableSeed {
			return ErrInvalidCanonicalMaterialization
		}
	}
	return nil
}

func canonicalMaterializationKinds(input []domain.ArtifactKind) ([]domain.ArtifactKind, error) {
	if len(input) == 0 {
		return nil, ErrInvalidCanonicalMaterialization
	}
	wanted := make(map[domain.ArtifactKind]struct{}, len(input))
	for _, kind := range input {
		if kind != domain.ArtifactKindStandings && kind != domain.ArtifactKindTopFour && kind != domain.ArtifactKindBracket {
			return nil, ErrInvalidCanonicalMaterialization
		}
		if _, duplicate := wanted[kind]; duplicate {
			return nil, ErrInvalidCanonicalMaterialization
		}
		wanted[kind] = struct{}{}
	}
	if _, standings := wanted[domain.ArtifactKindStandings]; !standings {
		return nil, ErrInvalidCanonicalMaterialization
	}
	ordered := []domain.ArtifactKind{domain.ArtifactKindStandings}
	for _, kind := range []domain.ArtifactKind{domain.ArtifactKindTopFour, domain.ArtifactKindBracket} {
		if _, found := wanted[kind]; found {
			ordered = append(ordered, kind)
		}
	}
	return ordered, nil
}

func canonicalSwissParticipants(
	input []CanonicalSwissParticipant,
) ([]uuid.UUID, []swissusecase.ParticipantSeed, error) {
	participants := make([]uuid.UUID, len(input))
	seeds := make([]swissusecase.ParticipantSeed, len(input))
	for index, participant := range input {
		if participant.ID == uuid.Nil || participant.StableSeed < 1 {
			return nil, nil, ErrInvalidCanonicalMaterialization
		}
		participants[index] = participant.ID
		seeds[index] = swissusecase.ParticipantSeed{ParticipantID: participant.ID, Seed: participant.StableSeed}
	}
	return participants, seeds, nil
}

func canonicalStandingsArtifact(
	standings []swissusecase.NormalStanding,
) (CanonicalMaterializedArtifact, error) {
	if len(standings) == 0 {
		return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
	}
	type entry struct {
		ParticipantID     uuid.UUID `json:"participant_id"`
		Position          int       `json:"position"`
		Points            int       `json:"points"`
		Wins              int       `json:"wins"`
		Losses            int       `json:"losses"`
		ByeCount          int       `json:"bye_count"`
		Buchholz          int       `json:"buchholz"`
		HeadToHeadPoints  int       `json:"head_to_head_points"`
		HeadToHeadApplied bool      `json:"head_to_head_applied"`
		EffectiveTime     int64     `json:"effective_time"`
		AcceptedSolveTime *int64    `json:"accepted_solve_time,omitempty"`
		StableSeed        int       `json:"stable_seed"`
	}
	payloadEntries := make([]entry, len(standings))
	members := make([]CanonicalMaterializedMember, len(standings))
	for index, standing := range standings {
		if standing.ParticipantID == uuid.Nil || standing.Position != index+1 || standing.Points < 0 ||
			standing.Wins < 0 || standing.Losses < 0 || standing.ByeCount < 0 || standing.Buchholz < 0 ||
			standing.EffectiveTime < 0 || standing.Seed < 1 {
			return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
		}
		var accepted *int64
		if standing.AcceptedSolveTime != nil {
			value := int64(*standing.AcceptedSolveTime)
			accepted = &value
		}
		payloadEntries[index] = entry{
			ParticipantID: standing.ParticipantID, Position: standing.Position, Points: standing.Points,
			Wins: standing.Wins, Losses: standing.Losses, ByeCount: standing.ByeCount,
			Buchholz: standing.Buchholz, HeadToHeadPoints: standing.HeadToHeadPoints,
			HeadToHeadApplied: standing.HeadToHeadApplied, EffectiveTime: int64(standing.EffectiveTime),
			AcceptedSolveTime: accepted, StableSeed: standing.Seed,
		}
		scoreMilli := int64(standing.Points) * 1000
		members[index] = CanonicalMaterializedMember{
			ParticipantID: standing.ParticipantID, Position: standing.Position, ScoreMilli: &scoreMilli,
		}
	}
	payload, err := json.Marshal(struct {
		Entries []entry `json:"entries"`
	}{Entries: payloadEntries})
	if err != nil {
		return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
	}
	return canonicalMaterializedArtifact(domain.ArtifactKindStandings, payload, members), nil
}

func canonicalTopFourArtifact(
	input CanonicalMaterializationInput,
	standings []swissusecase.NormalStanding,
) (CanonicalMaterializedArtifact, error) {
	if !input.SwissComplete || len(input.TopFour) != 4 || len(standings) < 4 {
		return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
	}
	standingByParticipant := make(map[uuid.UUID]swissusecase.NormalStanding, len(standings))
	for _, standing := range standings {
		standingByParticipant[standing.ParticipantID] = standing
	}
	positions := append([]CanonicalTopFourPosition(nil), input.TopFour...)
	sort.Slice(positions, func(first, second int) bool { return positions[first].Position < positions[second].Position })
	members := make([]CanonicalMaterializedMember, len(positions))
	participants := make([]uuid.UUID, len(positions))
	commits := make([]uuid.UUID, 0, len(positions))
	seenParticipants := make(map[uuid.UUID]struct{}, len(positions))
	seenCommits := make(map[uuid.UUID]struct{}, len(positions))
	for index, position := range positions {
		participantID := position.ParticipantID
		_, known := standingByParticipant[participantID]
		if !known || participantID == uuid.Nil || position.Position != index+1 {
			return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
		}
		if _, duplicate := seenParticipants[participantID]; duplicate {
			return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
		}
		seenParticipants[participantID] = struct{}{}
		if position.GoldenPositionCommitID == nil {
			if standings[index].ParticipantID != participantID {
				return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
			}
		} else if *position.GoldenPositionCommitID == uuid.Nil {
			return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
		} else if _, duplicate := seenCommits[*position.GoldenPositionCommitID]; duplicate {
			return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
		} else {
			seenCommits[*position.GoldenPositionCommitID] = struct{}{}
			commits = append(commits, *position.GoldenPositionCommitID)
		}
		participants[index] = participantID
		members[index] = CanonicalMaterializedMember{ParticipantID: participantID, Position: position.Position}
	}
	payload, err := json.Marshal(struct {
		Participants []uuid.UUID `json:"participants"`
	}{Participants: participants})
	if err != nil {
		return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
	}
	artifact := canonicalMaterializedArtifact(domain.ArtifactKindTopFour, payload, members)
	artifact.GoldenPositionCommitIDs = commits
	return artifact, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func canonicalBracketArtifact(
	input CanonicalMaterializationInput,
) (CanonicalMaterializedArtifact, error) {
	if len(input.TopFour) != 4 || len(input.Bracket) == 0 {
		return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
	}
	rounds := append([]CanonicalBracketMatch(nil), input.Bracket...)
	sort.Slice(rounds, func(first, second int) bool { return rounds[first].Position < rounds[second].Position })
	seen := make(map[int]struct{}, len(rounds))
	qualified := make(map[uuid.UUID]struct{}, len(input.TopFour))
	for _, position := range input.TopFour {
		qualified[position.ParticipantID] = struct{}{}
	}
	for index, match := range rounds {
		if match.Position != index+1 || match.SeriesID == uuid.Nil || match.FirstParticipantID == uuid.Nil ||
			match.SecondParticipantID == uuid.Nil || match.FirstParticipantID == match.SecondParticipantID ||
			!match.State.IsValid() || match.FirstWins < 0 || match.SecondWins < 0 {
			return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
		}
		if _, firstQualified := qualified[match.FirstParticipantID]; !firstQualified {
			return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
		}
		if _, secondQualified := qualified[match.SecondParticipantID]; !secondQualified {
			return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
		}
		if _, duplicate := seen[match.Position]; duplicate {
			return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
		}
		seen[match.Position] = struct{}{}
	}
	//nolint:musttag // This versioned application-owned document is validated on both encode and decode.
	payload, err := json.Marshal(struct {
		Rounds []CanonicalBracketMatch `json:"rounds"`
	}{Rounds: rounds})
	if err != nil {
		return CanonicalMaterializedArtifact{}, ErrInvalidCanonicalMaterialization
	}
	members := make([]CanonicalMaterializedMember, len(input.TopFour))
	for index, position := range input.TopFour {
		members[index] = CanonicalMaterializedMember{ParticipantID: position.ParticipantID, Position: position.Position}
	}
	return canonicalMaterializedArtifact(domain.ArtifactKindBracket, payload, members), nil
}

func canonicalMaterializedArtifact(
	kind domain.ArtifactKind,
	payload []byte,
	members []CanonicalMaterializedMember,
) CanonicalMaterializedArtifact {
	return CanonicalMaterializedArtifact{
		Kind: kind, Payload: payload, PayloadDigest: sha256.Sum256(payload), Members: members,
	}
}
