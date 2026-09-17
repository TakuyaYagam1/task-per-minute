package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	adminoperation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
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

func tournamentAdminPairingAuthority(
	header sqlc.LockTournamentPairingAuthorityRow,
	participantRows []sqlc.LockTournamentPairingParticipantsRow,
	historyRows []sqlc.LockTournamentPairingHistoryRow,
	byeRows []sqlc.LockTournamentPairingByesRow,
	roundRows []sqlc.LockTournamentPairingRoundsRow,
	waveRows []sqlc.LockTournamentPairingWavesRow,
) (tournamentadmin.PairingAuthority, error) {
	if header.TournamentID == uuid.Nil || header.RosterID == uuid.Nil || !header.RosterLockedAt.Valid {
		return tournamentadmin.PairingAuthority{}, domain.ErrInternal
	}
	participants := make([]tournamentadmin.PairingParticipant, 0, len(participantRows))
	for _, row := range participantRows {
		attendance := domain.AttendanceState(row.Attendance)
		if !attendance.IsValid() {
			return tournamentadmin.PairingAuthority{}, domain.ErrInternal
		}
		if attendance != domain.AttendanceStateCheckedIn {
			continue
		}
		participants = append(participants, tournamentadmin.PairingParticipant{
			ID: row.ID, StableSeed: int(row.Seed),
		})
	}
	standings, err := tournamentAdminStandings(header.StandingsPayload, participants)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, err
	}
	previous, counts, err := tournamentAdminPairingHistory(historyRows)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, err
	}
	receivedBye := make(map[uuid.UUID]bool, len(participants))
	for _, participant := range participants {
		receivedBye[participant.ID] = false
	}
	for _, row := range byeRows {
		if _, exists := receivedBye[row.ParticipantID]; !exists || receivedBye[row.ParticipantID] {
			return tournamentadmin.PairingAuthority{}, domain.ErrInternal
		}
		receivedBye[row.ParticipantID] = true
	}
	completed, err := tournamentAdminCompletedRoundCount(roundRows, waveRows)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, err
	}
	return tournamentadmin.PairingAuthority{
		TournamentID: header.TournamentID, TournamentState: domain.TournamentState(header.TournamentState),
		TournamentRevision: header.TournamentRevision, RosterID: header.RosterID,
		RosterRevision: header.RosterRevision, RosterLockedAt: header.RosterLockedAt.Time.UTC(),
		ProjectionRevisionID: header.ProjectionRevisionID, ProjectionRevision: header.ProjectionRevision,
		HistoryRevision: int64(len(historyRows)), Participants: participants, Standings: standings,
		PreviousMeetings: previous, PriorMeetingCounts: counts, ReceivedBye: receivedBye,
		RoundCount: len(roundRows), CompletedRoundCount: completed,
	}, nil
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

func tournamentAdminPairingHistory(
	rows []sqlc.LockTournamentPairingHistoryRow,
) ([]swissusecase.Pair, map[swissusecase.PairKey]int, error) {
	previous := make([]swissusecase.Pair, 0, len(rows))
	counts := make(map[swissusecase.PairKey]int, len(rows))
	for _, row := range rows {
		if row.FirstParticipantID == uuid.Nil || row.SecondParticipantID == uuid.Nil ||
			row.FirstParticipantID == row.SecondParticipantID || row.PriorMeetingCount < 0 {
			return nil, nil, domain.ErrInternal
		}
		key := swissusecase.NewPairKey(row.FirstParticipantID, row.SecondParticipantID)
		count := int(row.PriorMeetingCount) + 1
		if prior, exists := counts[key]; exists {
			if count <= prior {
				return nil, nil, domain.ErrInternal
			}
			counts[key] = count
			continue
		}
		counts[key] = count
		first, second := key.Participants()
		previous = append(previous, swissusecase.Pair{FirstParticipantID: first, SecondParticipantID: second})
	}
	return previous, counts, nil
}

func tournamentAdminCompletedRoundCount(
	rounds []sqlc.LockTournamentPairingRoundsRow,
	waves []sqlc.LockTournamentPairingWavesRow,
) (int, error) {
	waveByRound := make(map[uuid.UUID]domain.WaveState, len(waves))
	for _, row := range waves {
		if row.RoundID == uuid.Nil || row.WaveID == uuid.Nil {
			return 0, domain.ErrInternal
		}
		if _, duplicate := waveByRound[row.RoundID]; duplicate {
			return 0, domain.ErrInternal
		}
		waveByRound[row.RoundID] = domain.WaveState(row.State)
	}
	completed := 0
	for index, row := range rounds {
		if row.ID == uuid.Nil || row.RoundNumber != int16(index+1) || row.Revision < 1 {
			return 0, domain.ErrInternal
		}
		state, exists := waveByRound[row.ID]
		if !exists || !state.IsValid() {
			return 0, domain.ErrInternal
		}
		if !validTournamentAdminPlannedCurrentRound(index, len(rounds), row.LockedAt.Valid, state) {
			return 0, domain.ErrInternal
		}
		if state == domain.WaveStateCompleted {
			completed++
		}
	}
	if len(waveByRound) != len(rounds) {
		return 0, domain.ErrInternal
	}
	return completed, nil
}

func validTournamentAdminPlannedCurrentRound(
	index, roundCount int,
	locked bool,
	state domain.WaveState,
) bool {
	return locked || (index == roundCount-1 && state == domain.WaveStatePlanned)
}

func tournamentAdminPairingCommand(
	row sqlc.SwissPairingCommand,
) (*tournamentadmin.PairingCommandRecord, error) {
	digest, err := executionDigest(row.RequestDigest)
	if err != nil || !row.ExecutedAt.Valid {
		return nil, domain.ErrInternal
	}
	var categories []domain.Category
	if err := json.Unmarshal(row.Categories, &categories); err != nil || len(categories) == 0 {
		return nil, domain.ErrInternal
	}
	return &tournamentadmin.PairingCommandRecord{
		CommandScope: adminoperation.CommandScope{
			Operator:     adminoperation.OperatorIdentity{ActorID: row.ActorID},
			TournamentID: row.TournamentID, CommandID: row.CommandID,
		},
		RosterID: row.RosterID, RoundNumber: int(row.RoundNumber), Mode: tournamentadmin.PairingMode(row.PairingMode),
		CategoryMode: domain.CategoryMode(row.CategoryMode), Categories: categories,
		SourceProjectionRevisionID: row.SourceProjectionRevisionID,
		SourceProjectionRevision:   row.SourceProjectionRevision,
		SourceTournamentRevision:   row.SourceTournamentRevision, SourceRosterRevision: row.SourceRosterRevision,
		SourceHistoryRevision: row.SourceHistoryRevision, RequestDigest: digest,
		ResultDocument: append([]byte(nil), row.ResultDocument...), ExecutedAt: row.ExecutedAt.Time.UTC(),
	}, nil
}

func tournamentAdminSwissRoundView(
	plan tournamentadmin.PairingPlan,
	saved *SwissRoundRecord,
) (tournamentadmin.SwissRoundView, error) {
	if saved == nil || saved.ID != plan.RoundID {
		return tournamentadmin.SwissRoundView{}, domain.ErrInternal
	}
	view := tournamentadmin.SwissRoundView{
		ID: saved.ID, TournamentID: plan.Command.TournamentID, RoundNumber: saved.RoundNumber,
		Revision: saved.Revision, RosterParticipantIDs: pairingAuthorityParticipantIDs(plan.Authority),
		Pairings:  make([]tournamentadmin.SwissPairingView, len(plan.Pairs)),
		Standings: append([]tournamentadmin.SwissStandingView(nil), plan.Authority.Standings...),
		Locked:    saved.LockedAt != nil, LockedAt: utcTimePointer(saved.LockedAt), CreatedAt: saved.CreatedAt.UTC(),
		UpdatedAt: saved.UpdatedAt.UTC(),
	}
	if plan.Automatic != nil {
		evidence := tournamentAdminPairingEvidence(plan.Automatic.Evidence)
		view.PairingEvidence = &evidence
	}
	for index, pair := range plan.Pairs {
		pairing := tournamentadmin.SwissPairingView{
			ID: plan.PairingIDs[index], RoundID: plan.RoundID,
			FirstParticipantID: pair.FirstParticipantID, SecondParticipantID: pair.SecondParticipantID,
			EvidenceID: plan.PairingEvidenceID,
			Repeated:   tournamentAdminPairingRepeated(pair, plan.Authority.PriorMeetingCounts),
		}
		if pairing.Repeated {
			return tournamentadmin.SwissRoundView{}, domain.ErrInternal
		}
		view.Pairings[index] = pairing
	}
	if plan.Bye != nil {
		view.Bye = &tournamentadmin.SwissByeView{
			ID: tournamentAdminExecutionID(plan.Command.CommandID, "bye-record"), RoundID: plan.RoundID,
			ParticipantID: plan.Bye.ParticipantID, PointsAwarded: plan.Bye.PointsAwarded,
			RevisionID: tournamentAdminExecutionID(plan.Command.CommandID, "bye-revision"),
			EvidenceID: plan.Bye.Evidence.ID,
		}
	}
	return view, nil
}

func tournamentAdminPairingEvidence(evidence domain.DecisionEvidence) tournamentadmin.SwissPairingEvidenceView {
	return tournamentadmin.SwissPairingEvidenceView{
		ID: evidence.ID, Purpose: string(evidence.Purpose), AlgorithmVersion: evidence.AlgorithmVersion,
		NormalizedInputs: append([]string(nil), evidence.NormalizedInputs...),
		Result:           append([]string(nil), evidence.Result...), ReplayDigest: hex.EncodeToString(evidence.ReplayDigest[:]),
		OwnerID: evidence.OwnerID, DecidedAt: evidence.DecidedAt,
	}
}

func tournamentAdminWaveAuthority(
	header sqlc.LockTournamentAdminWaveAuthorityRow,
	members []sqlc.LockTournamentAdminWaveMembersRow,
	series []sqlc.LockTournamentAdminWaveSeriesRow,
	games []sqlc.LockTournamentAdminWaveGamesRow,
	assignments []sqlc.LockTournamentAdminWaveAssignmentsRow,
	deliveries []sqlc.LockTournamentAdminWaveDeliveriesRow,
) (tournamentadmin.WaveAuthority, error) {
	view, playableMembers, err := tournamentAdminWaveView(header, members, series)
	if err != nil {
		return tournamentadmin.WaveAuthority{}, err
	}
	graph, err := tournamentAdminWaveGraph(series, games, assignments, deliveries, playableMembers)
	if err != nil {
		return tournamentadmin.WaveAuthority{}, err
	}
	revisions := domain.ReadyWindowSourceRevisions{
		WaveRevisionID: domain.WaveRevisionID(header.RevisionID), WaveRevision: header.Revision,
		ProjectionRevisionID: header.ProjectionRevisionID, ProjectionRevision: header.ProjectionRevision,
	}
	if header.ArtifactRevisionID.Valid && header.ArtifactRevision != nil {
		revisions.ArtifactRevisionID = header.ArtifactRevisionID.UUID
		revisions.ArtifactRevision = *header.ArtifactRevision
	}
	return tournamentadmin.WaveAuthority{
		TournamentState:    domain.TournamentState(header.TournamentState),
		TournamentRevision: header.TournamentRevision, RosterID: header.RosterID,
		RosterRevision: header.RosterRevision, ProjectionRevisionID: header.ProjectionRevisionID,
		ProjectionRevision: header.ProjectionRevision, SourceRevisions: revisions, View: view, Graph: graph,
	}, nil
}

func tournamentAdminWaveView(
	header sqlc.LockTournamentAdminWaveAuthorityRow,
	members []sqlc.LockTournamentAdminWaveMembersRow,
	series []sqlc.LockTournamentAdminWaveSeriesRow,
) (tournamentadmin.WaveView, map[uuid.UUID]struct{}, error) {
	wave, err := tournamentAdminDomainWave(header)
	if err != nil {
		return tournamentadmin.WaveView{}, nil, err
	}
	mappedMembers, readiness, memberSet, err := tournamentAdminWaveMembers(members)
	if err != nil {
		return tournamentadmin.WaveView{}, nil, err
	}
	wave.Members = mappedMembers
	seriesIDs, playable, err := tournamentAdminWaveSeries(series, memberSet)
	if err != nil {
		return tournamentadmin.WaveView{}, nil, err
	}
	byeParticipantID, err := tournamentAdminWaveBye(header.ByeParticipantID, memberSet, playable)
	if err != nil {
		return tournamentadmin.WaveView{}, nil, err
	}
	view := tournamentadmin.WaveView{
		Wave: wave, Revision: header.Revision, ReadinessRevisions: readiness,
		SeriesIDs: seriesIDs, ByeParticipantID: byeParticipantID,
	}
	return view, playable, nil
}

func tournamentAdminDomainWave(header sqlc.LockTournamentAdminWaveAuthorityRow) (domain.Wave, error) {
	if header.ID == uuid.Nil || header.TournamentID == uuid.Nil || !header.CreatedAt.Valid || !header.UpdatedAt.Valid {
		return domain.Wave{}, domain.ErrInternal
	}
	wave := domain.Wave{
		ID: header.ID, TournamentID: header.TournamentID, RevisionID: domain.WaveRevisionID(header.RevisionID),
		State: domain.WaveState(header.State), StartedAt: utcNullableTime(header.StartedAt),
		PausedAt: utcNullableTime(header.PausedAt),
	}
	readyWindow, err := tournamentAdminReadyWindow(header)
	if err != nil {
		return domain.Wave{}, err
	}
	wave.ReadyWindow = readyWindow
	return wave, nil
}

func tournamentAdminReadyWindow(
	header sqlc.LockTournamentAdminWaveAuthorityRow,
) (*domain.ReadyWindow, error) {
	if !header.ReadyWindowID.Valid {
		return nil, nil
	}
	if !header.ReadyWindowRevisionID.Valid || header.ReadyWindowState == nil ||
		!header.OpenedAt.Valid || !header.Deadline.Valid {
		return nil, domain.ErrInternal
	}
	return &domain.ReadyWindow{
		ID: header.ReadyWindowID.UUID, WaveID: header.ID,
		RevisionID: domain.ReadyWindowRevisionID(header.ReadyWindowRevisionID.UUID),
		State:      domain.ReadyWindowState(*header.ReadyWindowState), OpenedAt: header.OpenedAt.Time.UTC(),
		Deadline: header.Deadline.Time.UTC(), ConsumedAt: utcNullableTime(header.ConsumedAt),
	}, nil
}

func tournamentAdminWaveMembers(
	rows []sqlc.LockTournamentAdminWaveMembersRow,
) ([]domain.WaveMember, map[uuid.UUID]int64, map[uuid.UUID]struct{}, error) {
	members := make([]domain.WaveMember, len(rows))
	readiness := make(map[uuid.UUID]int64, len(rows))
	memberSet := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		if row.ParticipantID == uuid.Nil || row.ReadinessRevision < 1 {
			return nil, nil, nil, domain.ErrInternal
		}
		if _, duplicate := memberSet[row.ParticipantID]; duplicate {
			return nil, nil, nil, domain.ErrInternal
		}
		memberSet[row.ParticipantID] = struct{}{}
		members[index] = domain.WaveMember{ParticipantID: row.ParticipantID, Ready: row.Ready}
		readiness[row.ParticipantID] = row.ReadinessRevision
	}
	return members, readiness, memberSet, nil
}

func tournamentAdminWaveSeries(
	rows []sqlc.LockTournamentAdminWaveSeriesRow,
	members map[uuid.UUID]struct{},
) (map[uuid.UUID]uuid.UUID, map[uuid.UUID]struct{}, error) {
	seriesIDs := make(map[uuid.UUID]uuid.UUID, len(rows)*2)
	playable := make(map[uuid.UUID]struct{}, len(rows)*2)
	for _, row := range rows {
		if row.ID == uuid.Nil || row.FirstParticipantID == row.SecondParticipantID {
			return nil, nil, domain.ErrInternal
		}
		for _, participantID := range []uuid.UUID{row.FirstParticipantID, row.SecondParticipantID} {
			if _, member := members[participantID]; !member {
				return nil, nil, domain.ErrInternal
			}
			if _, duplicate := playable[participantID]; duplicate {
				return nil, nil, domain.ErrInternal
			}
			playable[participantID] = struct{}{}
			seriesIDs[participantID] = row.ID
		}
	}
	return seriesIDs, playable, nil
}

func tournamentAdminWaveBye(
	bye uuid.NullUUID,
	members map[uuid.UUID]struct{},
	playable map[uuid.UUID]struct{},
) (*uuid.UUID, error) {
	if !bye.Valid {
		if len(playable) != len(members) {
			return nil, domain.ErrInternal
		}
		return nil, nil
	}
	if _, member := members[bye.UUID]; !member {
		return nil, domain.ErrInternal
	}
	if _, paired := playable[bye.UUID]; paired || len(playable)+1 != len(members) {
		return nil, domain.ErrInternal
	}
	participantID := bye.UUID
	return &participantID, nil
}

func tournamentAdminWaveGraph(
	series []sqlc.LockTournamentAdminWaveSeriesRow,
	games []sqlc.LockTournamentAdminWaveGamesRow,
	assignments []sqlc.LockTournamentAdminWaveAssignmentsRow,
	deliveries []sqlc.LockTournamentAdminWaveDeliveriesRow,
	playableMembers map[uuid.UUID]struct{},
) (tournamentadmin.WaveGraph, error) {
	graph := tournamentadmin.WaveGraph{SeriesCount: len(series), PlayableMemberCount: len(playableMembers)}
	seriesMembers, err := tournamentAdminSeriesGraph(&graph, series)
	if err != nil {
		return tournamentadmin.WaveGraph{}, err
	}
	gameSeries, err := tournamentAdminGameGraph(&graph, games, seriesMembers)
	if err != nil {
		return tournamentadmin.WaveGraph{}, err
	}
	graph.AssignmentCount, err = tournamentAdminAssignmentCount(assignments, gameSeries)
	if err != nil {
		return tournamentadmin.WaveGraph{}, err
	}
	graph.DeliveryMemberCount, err = tournamentAdminDeliveryCount(
		deliveries, gameSeries, seriesMembers, playableMembers,
	)
	if err != nil {
		return tournamentadmin.WaveGraph{}, err
	}
	return graph, nil
}

func tournamentAdminSeriesGraph(
	graph *tournamentadmin.WaveGraph,
	rows []sqlc.LockTournamentAdminWaveSeriesRow,
) (map[uuid.UUID]map[uuid.UUID]struct{}, error) {
	seriesMembers := make(map[uuid.UUID]map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		format := domain.SeriesFormat(row.Format)
		if row.ID == uuid.Nil || row.FirstParticipantID == uuid.Nil || row.SecondParticipantID == uuid.Nil ||
			row.FirstParticipantID == row.SecondParticipantID || row.Revision < 1 || !format.IsValid() {
			return nil, domain.ErrInternal
		}
		if _, duplicate := seriesMembers[row.ID]; duplicate {
			return nil, domain.ErrInternal
		}
		seriesMembers[row.ID] = map[uuid.UUID]struct{}{
			row.FirstParticipantID: {}, row.SecondParticipantID: {},
		}
		switch domain.SeriesState(row.State) {
		case domain.SeriesStateReady:
			graph.ReadySeriesCount++
		case domain.SeriesStateActive:
			graph.ActiveSeriesCount++
			if format == domain.SeriesFormatBO3 {
				graph.ContinuingSeriesCount++
			}
		case domain.SeriesStateTechnicalPause:
			graph.PausedSeriesCount++
		case domain.SeriesStateCompleted, domain.SeriesStateCancelled:
			graph.TerminalSeriesCount++
		case domain.SeriesStatePlanned, domain.SeriesStateLocked, domain.SeriesStateDraft,
			domain.SeriesStateReplayRequired:
		default:
			return nil, domain.ErrInternal
		}
	}
	return seriesMembers, nil
}

func tournamentAdminGameGraph(
	graph *tournamentadmin.WaveGraph,
	rows []sqlc.LockTournamentAdminWaveGamesRow,
	seriesMembers map[uuid.UUID]map[uuid.UUID]struct{},
) (map[uuid.UUID]uuid.UUID, error) {
	gameSeries := make(map[uuid.UUID]uuid.UUID, len(rows))
	seriesWithGame := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		if row.GameID == uuid.Nil || row.SeriesID == uuid.Nil || row.Revision < 1 {
			return nil, domain.ErrInternal
		}
		if _, exists := seriesMembers[row.SeriesID]; !exists {
			return nil, domain.ErrInternal
		}
		if _, duplicate := gameSeries[row.GameID]; duplicate {
			return nil, domain.ErrInternal
		}
		if _, duplicate := seriesWithGame[row.SeriesID]; duplicate {
			return nil, domain.ErrInternal
		}
		gameSeries[row.GameID] = row.SeriesID
		seriesWithGame[row.SeriesID] = struct{}{}
		switch domain.GameState(row.State) {
		case domain.GameStateReady:
			graph.ReadyGameCount++
		case domain.GameStateActive:
			graph.ActiveGameCount++
		case domain.GameStatePaused:
			graph.PausedGameCount++
		case domain.GameStateCompleted, domain.GameStateVoid, domain.GameStateCancelled,
			domain.GameStateSuperseded:
			graph.TerminalGameCount++
		case domain.GameStatePlanned:
		default:
			return nil, domain.ErrInternal
		}
	}
	graph.CurrentGameCount = len(gameSeries)
	return gameSeries, nil
}

func tournamentAdminAssignmentCount(
	rows []sqlc.LockTournamentAdminWaveAssignmentsRow,
	gameSeries map[uuid.UUID]uuid.UUID,
) (int, error) {
	assignedAttempts := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		if row.ID == uuid.Nil {
			return 0, domain.ErrInternal
		}
		if _, current := gameSeries[row.AttemptID]; !current || row.Revision < 1 {
			continue
		}
		if _, duplicate := assignedAttempts[row.AttemptID]; duplicate {
			return 0, domain.ErrInternal
		}
		assignedAttempts[row.AttemptID] = struct{}{}
	}
	return len(assignedAttempts), nil
}

func tournamentAdminDeliveryCount(
	rows []sqlc.LockTournamentAdminWaveDeliveriesRow,
	gameSeries map[uuid.UUID]uuid.UUID,
	seriesMembers map[uuid.UUID]map[uuid.UUID]struct{},
	playableMembers map[uuid.UUID]struct{},
) (int, error) {
	deliveredMembers := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		if row.ID == uuid.Nil || row.ParticipantID == uuid.Nil {
			return 0, domain.ErrInternal
		}
		seriesID, current := gameSeries[row.AttemptID]
		if !current {
			continue
		}
		if _, playable := playableMembers[row.ParticipantID]; !playable {
			return 0, domain.ErrInternal
		}
		if _, belongsToSeries := seriesMembers[seriesID][row.ParticipantID]; !belongsToSeries {
			return 0, domain.ErrInternal
		}
		if _, duplicate := deliveredMembers[row.ParticipantID]; duplicate {
			return 0, domain.ErrInternal
		}
		deliveredMembers[row.ParticipantID] = struct{}{}
	}
	return len(deliveredMembers), nil
}

func tournamentAdminWaveCommand(row sqlc.WaveControlCommand) (*tournamentadmin.WaveCommandRecord, error) {
	digest, err := executionDigest(row.RequestDigest)
	if err != nil || !row.ExecutedAt.Valid {
		return nil, domain.ErrInternal
	}
	var revisions domain.ReadyWindowSourceRevisions
	var graph tournamentadmin.WaveGraph
	//nolint:musttag // These JSON documents are application-owned command evidence validated below.
	if err := json.Unmarshal(row.SourceRevisions, &revisions); err != nil {
		return nil, domain.ErrInternal
	}
	//nolint:musttag // These JSON documents are application-owned command evidence validated below.
	if err := json.Unmarshal(row.SourceGraph, &graph); err != nil {
		return nil, domain.ErrInternal
	}
	resultDocument, normalPause := decodeTournamentAdminWaveResult(row.Action, row.ResultDocument)
	return &tournamentadmin.WaveCommandRecord{
		CommandScope: adminoperation.CommandScope{
			Operator:     adminoperation.OperatorIdentity{ActorID: row.ActorID},
			TournamentID: row.TournamentID, CommandID: row.CommandID,
		},
		RosterID: row.RosterID, WaveID: row.WaveID, Action: tournamentadmin.WaveAction(row.Action),
		SourceProjectionRevisionID: row.SourceProjectionRevisionID,
		SourceProjectionRevision:   row.SourceProjectionRevision,
		SourceTournamentRevision:   row.SourceTournamentRevision, SourceRosterRevision: row.SourceRosterRevision,
		SourceWaveRevision: row.SourceWaveRevision, ResultingWaveRevision: row.ResultingWaveRevision,
		SourceRevisions: revisions, SourceGraph: graph, RequestDigest: digest,
		Reason: stringPointerValue(row.Reason), ResultDocument: resultDocument, NormalPause: normalPause,
		ExecutedAt: row.ExecutedAt.Time.UTC(),
	}, nil
}

func executionDigest(value []byte) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(value) != len(digest) {
		return digest, domain.ErrInternal
	}
	copy(digest[:], value)
	return digest, nil
}

func pairingAuthorityParticipantIDs(authority tournamentadmin.PairingAuthority) []uuid.UUID {
	participants := append([]tournamentadmin.PairingParticipant(nil), authority.Participants...)
	sort.Slice(participants, func(i, j int) bool {
		if participants[i].StableSeed != participants[j].StableSeed {
			return participants[i].StableSeed < participants[j].StableSeed
		}
		return participants[i].ID.String() < participants[j].ID.String()
	})
	result := make([]uuid.UUID, len(participants))
	for index := range participants {
		result[index] = participants[index].ID
	}
	return result
}

func tournamentAdminManualPairingInputs(pairs []swissusecase.Pair) []string {
	result := make([]string, len(pairs))
	for index, pair := range pairs {
		first, second := swissusecase.NewPairKey(pair.FirstParticipantID, pair.SecondParticipantID).Participants()
		result[index] = first.String() + ":" + second.String()
	}
	slices.Sort(result)
	return result
}

func tournamentAdminByeParticipantID(bye *swissusecase.ByeSelection) uuid.UUID {
	if bye == nil {
		return uuid.Nil
	}
	return bye.ParticipantID
}

func nullableSwissByeParticipant(bye *swissusecase.ByeSelection) uuid.NullUUID {
	if bye == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: bye.ParticipantID, Valid: true}
}

func nullableSwissByeRevision(plan tournamentadmin.PairingPlan) uuid.NullUUID {
	if plan.Bye == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{
		UUID: tournamentAdminExecutionID(plan.Command.CommandID, "bye-revision"), Valid: true,
	}
}

func tournamentAdminPairingRepeated(pair swissusecase.Pair, prior map[swissusecase.PairKey]int) bool {
	return prior[swissusecase.NewPairKey(pair.FirstParticipantID, pair.SecondParticipantID)] > 0
}

func tournamentAdminExecutionID(namespace uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(role))
}

func optionalExecutionReason(reason string) *string {
	if reason == "" {
		return nil
	}
	return &reason
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
