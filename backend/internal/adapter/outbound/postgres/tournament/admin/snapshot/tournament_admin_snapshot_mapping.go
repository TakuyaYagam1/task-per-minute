package snapshot

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentadminexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
	adminoperation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	rostercapability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

type tournamentAdminSnapshotHeaderState struct {
	tournament usecase.TournamentView
	cursor     tournamentadmin.OperatorCursor
	authority  *authoritydomain.Identity
}

type tournamentAdminSnapshotSeriesGraph struct {
	values           []domain.Series
	byID             map[uuid.UUID]domain.Series
	revisionByID     map[uuid.UUID]int64
	gameRevisionByID map[uuid.UUID]int64
	gameRosterByID   map[uuid.UUID]uuid.UUID
	gameSeriesByID   map[uuid.UUID]uuid.UUID
	seriesRosterByID map[uuid.UUID]uuid.UUID
}

type tournamentAdminSnapshotMember struct {
	ready       bool
	revision    int64
	seriesID    uuid.UUID
	hasSeriesID bool
}

type tournamentAdminSnapshotWaveAssembly struct {
	views        []tournamentadminexecution.WaveView
	viewIndex    map[uuid.UUID]int
	memberOrder  map[uuid.UUID][]uuid.UUID
	memberValues map[uuid.UUID]map[uuid.UUID]tournamentAdminSnapshotMember
}

func tournamentAdminSnapshotHeader(
	row sqlc.GetTournamentAdminSnapshotHeaderRow,
	tournamentID uuid.UUID,
) (tournamentAdminSnapshotHeaderState, error) {
	if row.ProjectionRevision < 1 {
		return tournamentAdminSnapshotHeaderState{}, domain.ErrTournamentProjectionNotFound
	}
	view, err := tournamentAdminSnapshotTournament(row, tournamentID)
	if err != nil {
		return tournamentAdminSnapshotHeaderState{}, domain.ErrInternal
	}

	cursor := tournamentadmin.OperatorCursor{
		ProjectionRevision: row.ProjectionRevision,
		AuthorityRevision:  row.AuthorityRevision,
		AuditSequence:      row.AuditSequence,
	}
	if cursor.AuthorityRevision < 1 || cursor.AuditSequence < 0 {
		return tournamentAdminSnapshotHeaderState{}, domain.ErrInternal
	}
	authority, err := tournamentAdminSnapshotAuthority(row, view)
	if err != nil {
		return tournamentAdminSnapshotHeaderState{}, err
	}
	return tournamentAdminSnapshotHeaderState{tournament: view, cursor: cursor, authority: authority}, nil
}

func tournamentAdminSnapshotTournament(
	row sqlc.GetTournamentAdminSnapshotHeaderRow,
	tournamentID uuid.UUID,
) (usecase.TournamentView, error) {
	if row.ID != tournamentID || row.ID == uuid.Nil || !row.RosterID.Valid || row.RosterID.UUID == uuid.Nil ||
		row.RosterSize < 0 || int(row.RosterSize) > domain.TournamentMaxParticipants {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	view := usecase.TournamentView{
		ID: row.ID, RosterID: row.RosterID.UUID, Preset: domain.TournamentPreset(row.Preset),
		Name: row.Name, PublicID: row.PublicID, PlannedRosterSize: int(row.PlannedRosterSize),
		ContentRevision: row.ContentRevision,
		State:           domain.TournamentState(row.State), Revision: row.TournamentRevision, RosterSize: int(row.RosterSize),
		PausedFromState: lifecycleTournamentState(row.PausedFromState),
	}
	var valid bool
	view.CreatedAt, valid = lifecycleRequiredTime(row.TournamentCreatedAt.Valid, row.TournamentCreatedAt.Time)
	if !valid {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	view.UpdatedAt, valid = lifecycleRequiredTime(row.TournamentUpdatedAt.Valid, row.TournamentUpdatedAt.Time)
	if !valid {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	view.StartedAt, valid = lifecycleOptionalTime(row.TournamentStartedAt.Valid, row.TournamentStartedAt.Time)
	if !valid {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	view.FinishedAt, valid = lifecycleOptionalTime(row.TournamentFinishedAt.Valid, row.TournamentFinishedAt.Time)
	if !valid || !validLifecycleTournamentView(view, tournamentID) {
		return usecase.TournamentView{}, domain.ErrInternal
	}
	return view, nil
}

func tournamentAdminSnapshotAuthority(
	row sqlc.GetTournamentAdminSnapshotHeaderRow,
	view usecase.TournamentView,
) (*authoritydomain.Identity, error) {
	empty := row.AuthorityHolderID == uuid.Nil && row.AuthorityLeaseID == uuid.Nil &&
		row.AuthorityEpoch == 0 && row.AuthorityProcessKind == ""
	if empty {
		if row.AuthorityRevision != view.Revision {
			return nil, domain.ErrInternal
		}
		return nil, nil
	}
	identity := authoritydomain.Identity{
		TournamentID: view.ID,
		HolderID:     row.AuthorityHolderID,
		LeaseID:      row.AuthorityLeaseID,
		Epoch:        row.AuthorityEpoch,
		ProcessKind:  authoritydomain.ProcessKind(row.AuthorityProcessKind),
	}
	if identity.Validate() != nil || row.AuthorityRevision < identity.Epoch {
		return nil, domain.ErrInternal
	}
	return &identity, nil
}

func tournamentAdminSnapshotCursorError(
	requested *tournamentadmin.OperatorCursor,
	current tournamentadmin.OperatorCursor,
	state domain.TournamentState,
) error {
	if requested == nil || requested.ProjectionRevision <= current.ProjectionRevision &&
		requested.AuthorityRevision <= current.AuthorityRevision &&
		requested.AuditSequence <= current.AuditSequence {
		return nil
	}
	return &adminoperation.RevisionConflictError{
		ExpectedRevision: requested.ProjectionRevision,
		CurrentRevision:  current.ProjectionRevision,
		CurrentState:     state,
	}
}

func tournamentAdminSnapshotWaves(
	rows []sqlc.ListTournamentAdminSnapshotWavesRow,
	members []sqlc.ListTournamentAdminSnapshotWaveMembersRow,
	roster rostercapability.RosterView,
) ([]tournamentadminexecution.WaveView, error) {
	participantIDs := make(map[uuid.UUID]struct{}, len(roster.Participants))
	for _, participant := range roster.Participants {
		participantIDs[participant.ID] = struct{}{}
	}
	assembly, err := tournamentAdminSnapshotWaveHeaders(rows, roster.TournamentID)
	if err != nil {
		return nil, err
	}
	for _, row := range members {
		if !assembly.addMember(row, participantIDs) {
			return nil, domain.ErrInternal
		}
	}
	return assembly.finish(roster.TournamentID)
}

func tournamentAdminSnapshotWaveHeaders(
	rows []sqlc.ListTournamentAdminSnapshotWavesRow,
	tournamentID uuid.UUID,
) (tournamentAdminSnapshotWaveAssembly, error) {
	assembly := tournamentAdminSnapshotWaveAssembly{
		views:        make([]tournamentadminexecution.WaveView, len(rows)),
		viewIndex:    make(map[uuid.UUID]int, len(rows)),
		memberOrder:  make(map[uuid.UUID][]uuid.UUID, len(rows)),
		memberValues: make(map[uuid.UUID]map[uuid.UUID]tournamentAdminSnapshotMember, len(rows)),
	}
	for index, row := range rows {
		if _, duplicate := assembly.viewIndex[row.ID]; row.ID == uuid.Nil || duplicate {
			return tournamentAdminSnapshotWaveAssembly{}, domain.ErrInternal
		}
		wave, err := tournamentAdminSnapshotWave(row, tournamentID)
		if err != nil {
			return tournamentAdminSnapshotWaveAssembly{}, err
		}
		assembly.views[index] = tournamentadminexecution.WaveView{
			Wave:               wave,
			Revision:           row.Revision,
			ReadinessRevisions: make(map[uuid.UUID]int64),
			SeriesIDs:          make(map[uuid.UUID]uuid.UUID),
		}
		if row.ByeParticipantID.Valid {
			if row.ByeParticipantID.UUID == uuid.Nil {
				return tournamentAdminSnapshotWaveAssembly{}, domain.ErrInternal
			}
			byeID := row.ByeParticipantID.UUID
			assembly.views[index].ByeParticipantID = &byeID
		}
		assembly.viewIndex[row.ID] = index
		assembly.memberValues[row.ID] = make(map[uuid.UUID]tournamentAdminSnapshotMember)
	}
	return assembly, nil
}

func (assembly *tournamentAdminSnapshotWaveAssembly) addMember(
	row sqlc.ListTournamentAdminSnapshotWaveMembersRow,
	participantIDs map[uuid.UUID]struct{},
) bool {
	_, waveExists := assembly.viewIndex[row.WaveID]
	_, participantExists := participantIDs[row.ParticipantID]
	if !waveExists || !participantExists || row.ParticipantID == uuid.Nil || row.Ready == nil ||
		row.ReadinessRevision == nil || *row.ReadinessRevision < 1 {
		return false
	}
	byParticipant := assembly.memberValues[row.WaveID]
	member, known := byParticipant[row.ParticipantID]
	if !known {
		member = tournamentAdminSnapshotMember{ready: *row.Ready, revision: *row.ReadinessRevision}
		assembly.memberOrder[row.WaveID] = append(assembly.memberOrder[row.WaveID], row.ParticipantID)
	} else if member.ready != *row.Ready || member.revision != *row.ReadinessRevision {
		return false
	}
	if row.SeriesID != uuid.Nil {
		if member.hasSeriesID && member.seriesID != row.SeriesID {
			return false
		}
		member.seriesID = row.SeriesID
		member.hasSeriesID = true
	}
	byParticipant[row.ParticipantID] = member
	return true
}

func (assembly tournamentAdminSnapshotWaveAssembly) finish(
	tournamentID uuid.UUID,
) ([]tournamentadminexecution.WaveView, error) {
	for index := range assembly.views {
		view := &assembly.views[index]
		order := assembly.memberOrder[view.Wave.ID]
		view.Wave.Members = make([]domain.WaveMember, len(order))
		for memberIndex, participantID := range order {
			member := assembly.memberValues[view.Wave.ID][participantID]
			view.Wave.Members[memberIndex] = domain.WaveMember{ParticipantID: participantID, Ready: member.ready}
			view.ReadinessRevisions[participantID] = member.revision
			if member.hasSeriesID {
				view.SeriesIDs[participantID] = member.seriesID
			}
		}
		if !tournamentAdminSnapshotWaveViewValid(*view, tournamentID) {
			return nil, domain.ErrInternal
		}
	}
	return assembly.views, nil
}

func tournamentAdminSnapshotWave(
	row sqlc.ListTournamentAdminSnapshotWavesRow,
	tournamentID uuid.UUID,
) (domain.Wave, error) {
	if row.ID == uuid.Nil || row.TournamentID != tournamentID || row.RevisionID == uuid.Nil || row.Revision < 1 {
		return domain.Wave{}, domain.ErrInternal
	}
	createdAt, valid := tournamentAdminSnapshotRequiredTime(row.CreatedAt)
	if !valid {
		return domain.Wave{}, domain.ErrInternal
	}
	updatedAt, valid := tournamentAdminSnapshotRequiredTime(row.UpdatedAt)
	if !valid || updatedAt.Before(createdAt) {
		return domain.Wave{}, domain.ErrInternal
	}
	startedAt, valid := tournamentAdminSnapshotOptionalTime(row.StartedAt)
	if !valid {
		return domain.Wave{}, domain.ErrInternal
	}
	pausedAt, valid := tournamentAdminSnapshotOptionalTime(row.PausedAt)
	if !valid {
		return domain.Wave{}, domain.ErrInternal
	}
	closedAt, valid := tournamentAdminSnapshotOptionalTime(row.ClosedAt)
	if !valid || !tournamentAdminSnapshotWaveTimeline(
		domain.WaveState(row.State), createdAt, updatedAt, startedAt, pausedAt, closedAt,
	) {
		return domain.Wave{}, domain.ErrInternal
	}
	window, err := tournamentAdminSnapshotReadyWindow(row)
	if err != nil {
		return domain.Wave{}, err
	}
	return domain.Wave{
		ID:           row.ID,
		TournamentID: tournamentID,
		RevisionID:   domain.WaveRevisionID(row.RevisionID),
		State:        domain.WaveState(row.State),
		ReadyWindow:  window,
		StartedAt:    startedAt,
		PausedAt:     pausedAt,
	}, nil
}

func tournamentAdminSnapshotReadyWindow(
	row sqlc.ListTournamentAdminSnapshotWavesRow,
) (*domain.ReadyWindow, error) {
	if !row.ReadyWindowID.Valid {
		if row.ReadyWindowRevisionID.Valid || row.ReadyWindowState != nil || row.ReadyWindowOpenedAt.Valid ||
			row.ReadyWindowDeadline.Valid || row.ReadyWindowConsumedAt.Valid {
			return nil, domain.ErrInternal
		}
		return nil, nil
	}
	if row.ReadyWindowID.UUID == uuid.Nil || !row.ReadyWindowRevisionID.Valid ||
		row.ReadyWindowRevisionID.UUID == uuid.Nil || row.ReadyWindowState == nil {
		return nil, domain.ErrInternal
	}
	openedAt, valid := tournamentAdminSnapshotRequiredTime(row.ReadyWindowOpenedAt)
	if !valid {
		return nil, domain.ErrInternal
	}
	deadline, valid := tournamentAdminSnapshotRequiredTime(row.ReadyWindowDeadline)
	if !valid {
		return nil, domain.ErrInternal
	}
	consumedAt, valid := tournamentAdminSnapshotOptionalTime(row.ReadyWindowConsumedAt)
	if !valid {
		return nil, domain.ErrInternal
	}
	window := &domain.ReadyWindow{
		ID:         row.ReadyWindowID.UUID,
		WaveID:     row.ID,
		RevisionID: domain.ReadyWindowRevisionID(row.ReadyWindowRevisionID.UUID),
		State:      domain.ReadyWindowState(*row.ReadyWindowState),
		OpenedAt:   openedAt,
		Deadline:   deadline,
		ConsumedAt: consumedAt,
	}
	if window.Validate() != nil {
		return nil, domain.ErrInternal
	}
	return window, nil
}

//nolint:gocyclo // The state table audits every nullable timestamp combination as one invariant.
func tournamentAdminSnapshotWaveTimeline(
	state domain.WaveState,
	createdAt time.Time,
	updatedAt time.Time,
	startedAt *time.Time,
	pausedAt *time.Time,
	closedAt *time.Time,
) bool {
	for _, value := range []*time.Time{startedAt, pausedAt, closedAt} {
		if value != nil && (value.Before(createdAt) || value.After(updatedAt)) {
			return false
		}
	}
	switch state {
	case domain.WaveStatePlanned, domain.WaveStateReadyWindowOpen, domain.WaveStateReady,
		domain.WaveStateReadyWindowExpired:
		return startedAt == nil && pausedAt == nil && closedAt == nil
	case domain.WaveStateActive:
		return startedAt != nil && pausedAt == nil && closedAt == nil
	case domain.WaveStatePaused:
		return startedAt != nil && pausedAt != nil && closedAt == nil && !pausedAt.Before(*startedAt)
	case domain.WaveStateCompleted:
		return startedAt != nil && pausedAt == nil && closedAt != nil && !closedAt.Before(*startedAt)
	case domain.WaveStateSuperseded:
		return pausedAt == nil && closedAt != nil
	default:
		return false
	}
}

//nolint:gocyclo // Swiss pairing, explicit bye, readiness, and membership cardinality form one graph boundary.
func tournamentAdminSnapshotWaveViewValid(
	view tournamentadminexecution.WaveView,
	tournamentID uuid.UUID,
) bool {
	if view.Revision < 1 || view.Wave.TournamentID != tournamentID || view.Wave.Validate() != nil ||
		view.ReadinessRevisions == nil || view.SeriesIDs == nil ||
		len(view.ReadinessRevisions) != len(view.Wave.Members) {
		return false
	}
	memberSet := make(map[uuid.UUID]struct{}, len(view.Wave.Members))
	seriesCounts := make(map[uuid.UUID]int, len(view.SeriesIDs))
	for _, member := range view.Wave.Members {
		memberSet[member.ParticipantID] = struct{}{}
		if view.ReadinessRevisions[member.ParticipantID] < 1 {
			return false
		}
		seriesID, paired := view.SeriesIDs[member.ParticipantID]
		if view.ByeParticipantID != nil && *view.ByeParticipantID == member.ParticipantID {
			if paired {
				return false
			}
			continue
		}
		if !paired || seriesID == uuid.Nil {
			return false
		}
		seriesCounts[seriesID]++
	}
	if view.ByeParticipantID == nil {
		if len(view.SeriesIDs) != len(view.Wave.Members) {
			return false
		}
	} else {
		if *view.ByeParticipantID == uuid.Nil || len(view.SeriesIDs)+1 != len(view.Wave.Members) {
			return false
		}
		if _, exists := memberSet[*view.ByeParticipantID]; !exists {
			return false
		}
	}
	for _, count := range seriesCounts {
		if count != 2 {
			return false
		}
	}
	return true
}

func tournamentAdminSnapshotSeries(
	rows []sqlc.Series,
	slots []sqlc.GameSlot,
	attempts []sqlc.GameAttempt,
	roster rostercapability.RosterView,
) (tournamentAdminSnapshotSeriesGraph, error) {
	graph := tournamentAdminSnapshotSeriesGraph{
		values:           make([]domain.Series, len(rows)),
		byID:             make(map[uuid.UUID]domain.Series, len(rows)),
		revisionByID:     make(map[uuid.UUID]int64, len(rows)),
		gameRevisionByID: make(map[uuid.UUID]int64, len(attempts)),
		gameRosterByID:   make(map[uuid.UUID]uuid.UUID, len(attempts)),
		gameSeriesByID:   make(map[uuid.UUID]uuid.UUID, len(attempts)),
		seriesRosterByID: make(map[uuid.UUID]uuid.UUID, len(rows)),
	}
	participantIDs := make(map[uuid.UUID]struct{}, len(roster.Participants))
	for _, participant := range roster.Participants {
		participantIDs[participant.ID] = struct{}{}
	}
	rowByID, err := tournamentAdminSnapshotSeriesRows(rows, roster, participantIDs)
	if err != nil {
		return tournamentAdminSnapshotSeriesGraph{}, err
	}
	slotsBySeries, slotByID, err := tournamentAdminSnapshotSlotRows(slots, roster.ID, rowByID)
	if err != nil {
		return tournamentAdminSnapshotSeriesGraph{}, err
	}
	attemptsBySlot, err := tournamentAdminSnapshotAttemptRows(attempts, slotByID, &graph)
	if err != nil {
		return tournamentAdminSnapshotSeriesGraph{}, err
	}

	for index, row := range rows {
		series, err := tournamentAdminSnapshotDomainSeries(row, slotsBySeries[row.ID], attemptsBySlot)
		if err != nil {
			return tournamentAdminSnapshotSeriesGraph{}, err
		}
		graph.values[index] = series
		graph.byID[row.ID] = series
		graph.revisionByID[row.ID] = row.Revision
		graph.seriesRosterByID[row.ID] = row.RosterID
	}
	return graph, nil
}

func tournamentAdminSnapshotSeriesRows(
	rows []sqlc.Series,
	roster rostercapability.RosterView,
	participantIDs map[uuid.UUID]struct{},
) (map[uuid.UUID]sqlc.Series, error) {
	result := make(map[uuid.UUID]sqlc.Series, len(rows))
	for _, row := range rows {
		_, firstExists := participantIDs[row.FirstParticipantID]
		_, secondExists := participantIDs[row.SecondParticipantID]
		_, duplicate := result[row.ID]
		if row.ID == uuid.Nil || duplicate || row.TournamentID != roster.TournamentID ||
			row.RosterID != roster.ID || row.Revision < 1 || !firstExists || !secondExists ||
			!tournamentAdminSnapshotSeriesTimeline(row) {
			return nil, domain.ErrInternal
		}
		result[row.ID] = row
	}
	return result, nil
}

func tournamentAdminSnapshotSlotRows(
	rows []sqlc.GameSlot,
	rosterID uuid.UUID,
	seriesRows map[uuid.UUID]sqlc.Series,
) (map[uuid.UUID][]sqlc.GameSlot, map[uuid.UUID]sqlc.GameSlot, error) {
	bySeries := make(map[uuid.UUID][]sqlc.GameSlot, len(seriesRows))
	byID := make(map[uuid.UUID]sqlc.GameSlot, len(rows))
	for _, row := range rows {
		seriesRow, exists := seriesRows[row.SeriesID]
		_, duplicate := byID[row.ID]
		if !exists || row.ID == uuid.Nil || duplicate || row.RosterID != rosterID ||
			row.RosterID != seriesRow.RosterID || row.Revision < 1 ||
			!tournamentAdminSnapshotStoredTimeline(row.CreatedAt, row.UpdatedAt) {
			return nil, nil, domain.ErrInternal
		}
		byID[row.ID] = row
		bySeries[row.SeriesID] = append(bySeries[row.SeriesID], row)
	}
	return bySeries, byID, nil
}

func tournamentAdminSnapshotAttemptRows(
	rows []sqlc.GameAttempt,
	slotRows map[uuid.UUID]sqlc.GameSlot,
	graph *tournamentAdminSnapshotSeriesGraph,
) (map[uuid.UUID][]sqlc.GameAttempt, error) {
	bySlot := make(map[uuid.UUID][]sqlc.GameAttempt, len(slotRows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		slotRow, exists := slotRows[row.SlotID]
		_, duplicate := seen[row.ID]
		if !exists || row.ID == uuid.Nil || duplicate || row.SeriesID != slotRow.SeriesID ||
			row.RosterID != slotRow.RosterID || row.Revision < 1 || !tournamentAdminSnapshotGameTimeline(row) {
			return nil, domain.ErrInternal
		}
		seen[row.ID] = struct{}{}
		bySlot[row.SlotID] = append(bySlot[row.SlotID], row)
		graph.gameRevisionByID[row.ID] = row.Revision
		graph.gameRosterByID[row.ID] = row.RosterID
		graph.gameSeriesByID[row.ID] = row.SeriesID
	}
	return bySlot, nil
}

func tournamentAdminSnapshotDomainSeries(
	row sqlc.Series,
	slotRows []sqlc.GameSlot,
	attemptsBySlot map[uuid.UUID][]sqlc.GameAttempt,
) (domain.Series, error) {
	series := domain.Series{
		ID:                  row.ID,
		TournamentID:        row.TournamentID,
		FirstParticipantID:  row.FirstParticipantID,
		SecondParticipantID: row.SecondParticipantID,
		Format:              domain.SeriesFormat(row.Format),
		State:               domain.SeriesState(row.State),
		Score: domain.SeriesScore{
			FirstParticipantWins:  int(row.FirstParticipantWins),
			SecondParticipantWins: int(row.SecondParticipantWins),
		},
		Slots: make([]domain.GameSlot, len(slotRows)),
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		series.WinnerID = &winnerID
	}
	if row.CurrentScoreRevisionID.Valid {
		revisionID := domain.SeriesScoreRevisionID(row.CurrentScoreRevisionID.UUID)
		series.CurrentScoreRevisionID = &revisionID
	}
	if row.CurrentResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(row.CurrentResultRevisionID.UUID)
		series.CurrentResultRevisionID = &revisionID
	}
	for index, slotRow := range slotRows {
		slot := domain.GameSlot{
			ID:       slotRow.ID,
			SeriesID: slotRow.SeriesID,
			Position: int(slotRow.SlotNumber),
			Category: domain.Category(slotRow.Category),
			ScoreBefore: domain.SeriesScore{
				FirstParticipantWins:  int(slotRow.FirstParticipantWinsBefore),
				SecondParticipantWins: int(slotRow.SecondParticipantWinsBefore),
			},
			Attempts: make([]domain.Game, len(attemptsBySlot[slotRow.ID])),
		}
		for attemptIndex, attemptRow := range attemptsBySlot[slotRow.ID] {
			game, err := recoveryGame(attemptRow)
			if err != nil {
				return domain.Series{}, domain.ErrInternal
			}
			slot.Attempts[attemptIndex] = game
		}
		if slot.Validate() != nil {
			return domain.Series{}, domain.ErrInternal
		}
		series.Slots[index] = slot
	}
	if series.Validate() != nil {
		return domain.Series{}, domain.ErrInternal
	}
	return series, nil
}

func tournamentAdminSnapshotWaveSeriesMatch(
	waves []tournamentadminexecution.WaveView,
	seriesGraph tournamentAdminSnapshotSeriesGraph,
) bool {
	for _, wave := range waves {
		for participantID, seriesID := range wave.SeriesIDs {
			series, exists := seriesGraph.byID[seriesID]
			if !exists || participantID != series.FirstParticipantID && participantID != series.SecondParticipantID {
				return false
			}
			other := series.FirstParticipantID
			if other == participantID {
				other = series.SecondParticipantID
			}
			if wave.SeriesIDs[other] != seriesID {
				return false
			}
		}
	}
	return true
}

func tournamentAdminSnapshotSeriesTimeline(row sqlc.Series) bool {
	createdAt, valid := tournamentAdminSnapshotRequiredTime(row.CreatedAt)
	if !valid {
		return false
	}
	updatedAt, valid := tournamentAdminSnapshotRequiredTime(row.UpdatedAt)
	if !valid || updatedAt.Before(createdAt) {
		return false
	}
	startedAt, valid := tournamentAdminSnapshotOptionalTime(row.StartedAt)
	if !valid {
		return false
	}
	finishedAt, valid := tournamentAdminSnapshotOptionalTime(row.FinishedAt)
	if !valid {
		return false
	}
	for _, value := range []*time.Time{startedAt, finishedAt} {
		if value != nil && (value.Before(createdAt) || value.After(updatedAt)) {
			return false
		}
	}
	state := domain.SeriesState(row.State)
	if state.IsTerminal() {
		return finishedAt != nil && (startedAt == nil || !finishedAt.Before(*startedAt))
	}
	return finishedAt == nil
}

func tournamentAdminSnapshotGameTimeline(row sqlc.GameAttempt) bool {
	if !tournamentAdminSnapshotStoredTimeline(row.CreatedAt, row.UpdatedAt) {
		return false
	}
	createdAt, _ := tournamentAdminSnapshotRequiredTime(row.CreatedAt)
	updatedAt, _ := tournamentAdminSnapshotRequiredTime(row.UpdatedAt)
	startedAt, valid := tournamentAdminSnapshotOptionalTime(row.StartedAt)
	if !valid {
		return false
	}
	finishedAt, valid := tournamentAdminSnapshotOptionalTime(row.FinishedAt)
	if !valid {
		return false
	}
	for _, value := range []*time.Time{startedAt, finishedAt} {
		if value != nil && (value.Before(createdAt) || value.After(updatedAt)) {
			return false
		}
	}
	state := domain.GameState(row.State)
	if state.IsTerminal() {
		return finishedAt != nil && (startedAt == nil || !finishedAt.Before(*startedAt))
	}
	return finishedAt == nil
}

func tournamentAdminSnapshotStoredTimeline(created, updated pgtype.Timestamptz) bool {
	createdAt, valid := tournamentAdminSnapshotRequiredTime(created)
	if !valid {
		return false
	}
	updatedAt, valid := tournamentAdminSnapshotRequiredTime(updated)
	return valid && !updatedAt.Before(createdAt)
}

func tournamentAdminSnapshotRequiredTime(value pgtype.Timestamptz) (time.Time, bool) {
	return lifecycleRequiredTime(value.Valid, value.Time)
}

func tournamentAdminSnapshotOptionalTime(value pgtype.Timestamptz) (*time.Time, bool) {
	return lifecycleOptionalTime(value.Valid, value.Time)
}

func tournamentAdminSnapshotCurrentGame(series domain.Series) (*domain.Game, bool) {
	for slotIndex := len(series.Slots) - 1; slotIndex >= 0; slotIndex-- {
		slot := series.Slots[slotIndex]
		if len(slot.Attempts) == 0 {
			continue
		}
		game := slot.Attempts[len(slot.Attempts)-1]
		return &game, true
	}
	return nil, false
}
