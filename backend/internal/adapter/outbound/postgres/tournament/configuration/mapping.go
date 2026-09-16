package configuration

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

type tournamentAdminStandingsDocument struct {
	Entries []tournamentAdminStanding `json:"entries"`
}

type tournamentAdminStanding struct {
	ParticipantID       uuid.UUID `json:"participant_id"`
	Position            int       `json:"position"`
	Points              int       `json:"points"`
	Buchholz            int       `json:"buchholz"`
	HeadToHeadPoints    int       `json:"head_to_head_points,omitempty"`
	HeadToHeadApplied   bool      `json:"head_to_head_applied,omitempty"`
	EffectiveTimeNS     int64     `json:"effective_time"`
	AcceptedSolveTimeNS *int64    `json:"accepted_solve_time,omitempty"`
}

func tournamentAdminStandings(
	payload []byte,
	participants []tournamentadmin.PairingParticipant,
) ([]tournamentadmin.SwissStandingView, error) {
	var document tournamentAdminStandingsDocument
	if err := json.Unmarshal(payload, &document); err != nil || document.Entries == nil {
		return nil, domain.ErrInternal
	}
	if len(document.Entries) == 0 && len(participants) > 0 {
		return tournamentAdminInitialStandings(participants), nil
	}
	if len(document.Entries) != len(participants) {
		return nil, domain.ErrInternal
	}
	seeds := make(map[uuid.UUID]int, len(participants))
	for _, participant := range participants {
		seeds[participant.ID] = participant.StableSeed
	}
	result := make([]tournamentadmin.SwissStandingView, len(document.Entries))
	seen := make(map[uuid.UUID]struct{}, len(document.Entries))
	for index, entry := range document.Entries {
		seed, exists := seeds[entry.ParticipantID]
		if !exists || !validTournamentAdminStanding(entry) {
			return nil, domain.ErrInternal
		}
		if _, duplicate := seen[entry.ParticipantID]; duplicate {
			return nil, domain.ErrInternal
		}
		seen[entry.ParticipantID] = struct{}{}
		acceptedSolveTimeMS, valid := tournamentAdminSolveTime(entry.AcceptedSolveTimeNS)
		if !valid {
			return nil, domain.ErrInternal
		}
		result[index] = tournamentadmin.SwissStandingView{
			ParticipantID: entry.ParticipantID, Position: entry.Position, Points: entry.Points,
			PointsLabel: "provisional", Buchholz: entry.Buchholz, BuchholzStatus: "provisional",
			HeadToHeadPoints: entry.HeadToHeadPoints, HeadToHeadApplied: entry.HeadToHeadApplied,
			EffectiveTimeMS:     entry.EffectiveTimeNS / int64(time.Millisecond),
			AcceptedSolveTimeMS: acceptedSolveTimeMS, StableSeed: seed,
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Position < result[j].Position })
	for index := range result {
		if result[index].Position != index+1 {
			return nil, domain.ErrInternal
		}
	}
	return result, nil
}

func tournamentAdminInitialStandings(
	participants []tournamentadmin.PairingParticipant,
) []tournamentadmin.SwissStandingView {
	ordered := append([]tournamentadmin.PairingParticipant(nil), participants...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StableSeed != ordered[j].StableSeed {
			return ordered[i].StableSeed < ordered[j].StableSeed
		}
		return ordered[i].ID.String() < ordered[j].ID.String()
	})
	standings := make([]tournamentadmin.SwissStandingView, len(ordered))
	for index, participant := range ordered {
		standings[index] = tournamentadmin.SwissStandingView{
			ParticipantID: participant.ID, Position: index + 1,
			PointsLabel: "provisional", BuchholzStatus: "provisional",
			StableSeed: participant.StableSeed,
		}
	}
	return standings
}

func validTournamentAdminStanding(entry tournamentAdminStanding) bool {
	return entry.ParticipantID != uuid.Nil && entry.Position >= 1 && entry.Points >= 0 &&
		entry.Buchholz >= 0 && entry.HeadToHeadPoints >= 0 && entry.EffectiveTimeNS >= 0
}

func tournamentAdminSolveTime(valueNS *int64) (*int64, bool) {
	if valueNS == nil {
		return nil, true
	}
	if *valueNS < 0 {
		return nil, false
	}
	valueMS := *valueNS / int64(time.Millisecond)
	return &valueMS, true
}

type configurationSwissRoundMeta struct {
	ID         uuid.UUID
	RosterID   uuid.UUID
	RecordedAt time.Time
}

func configurationByeParams(
	meta configurationSwissRoundMeta,
	bye swissusecase.ByeSelection,
) (sqlc.CreateSwissByeParams, error) {
	if _, err := swissusecase.ReplayBye(bye); err != nil {
		return sqlc.CreateSwissByeParams{}, err
	}
	inputs, err := configurationMarshalJSON("SwissPostgres - bye - decision inputs", bye.Evidence.NormalizedInputs)
	if err != nil {
		return sqlc.CreateSwissByeParams{}, err
	}
	result, err := configurationMarshalJSON("SwissPostgres - bye - decision result", bye.Evidence.Result)
	if err != nil {
		return sqlc.CreateSwissByeParams{}, err
	}
	return sqlc.CreateSwissByeParams{
		RoundID:                  meta.ID,
		RosterID:                 meta.RosterID,
		ParticipantID:            bye.ParticipantID,
		PointsAwarded:            int16(swissusecase.PairingByePoints),
		DecisionEvidenceID:       bye.Evidence.ID,
		DecisionAlgorithmVersion: bye.Evidence.AlgorithmVersion,
		DecisionInputs:           inputs,
		DecisionSeed:             append([]byte(nil), bye.Evidence.Seed[:]...),
		DecisionResult:           result,
		DecisionReplayDigest:     append([]byte(nil), bye.Evidence.ReplayDigest[:]...),
		DecisionOwnerID:          bye.Evidence.OwnerID,
		DecidedAt:                validTimestamp(bye.Evidence.DecidedAt),
		CreatedAt:                validTimestamp(meta.RecordedAt),
	}, nil
}

func configurationMarshalJSON(operation string, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return []byte("[]"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s - marshal JSON: %w", operation, err)
	}
	return data, nil
}
