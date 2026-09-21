package roster

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	rostercapability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func (r *TournamentAdminRosterPostgres) LoadPreflightInput(
	ctx context.Context,
	authority rostercapability.RosterAuthority,
	evaluatedAt time.Time,
) (tournamentpreflight.ReportInput, error) {
	if !r.rosterWriteReady(ctx) || !validServerTime(evaluatedAt) {
		return tournamentpreflight.ReportInput{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	participants, err := loadTournamentPreflightParticipants(ctx, querier, authority)
	if err != nil {
		return tournamentpreflight.ReportInput{}, err
	}
	loadedContent, err := loadTournamentPreflightContent(ctx, querier, authority.Roster.TournamentID)
	if err != nil {
		return tournamentpreflight.ReportInput{}, err
	}
	content := loadedContent.configuration
	pairingRevision, pairings, repeated, overrides, byeID, err := loadTournamentPreflightPairings(
		ctx, querier, authority.Roster.ID, participants, evaluatedAt,
	)
	if err != nil {
		return tournamentpreflight.ReportInput{}, err
	}
	rosterSize := len(participants)
	return tournamentpreflight.ReportInput{
		RosterRevision: authority.Roster.Revision, PairingRevision: pairingRevision,
		Structural: tournamentpreflight.StructuralInput{
			TournamentID: authority.Roster.TournamentID, Preset: authority.TournamentPreset,
			ExpectedRosterSize: authority.PlannedRosterSize, Participants: participants,
			CategoryPools: content.CategoryPools,
			Pairings:      pairings, ByeParticipantID: byeID,
			RepeatedPairings: repeated, Overrides: overrides,
		},
		TaskHealth: loadedContent.taskHealth,
		Runtime: tournamentPreflightRuntime(
			authority, evaluatedAt, rosterSize, content, loadedContent.taskVersions,
		),
	}, nil
}

type tournamentPreflightContent struct {
	configuration domain.ContentConfiguration
	taskHealth    tournamentpreflight.TaskHealthInput
	taskVersions  []capacity.TaskVersion
}

// PreflightContent is the published content authority consumed by execution
// and terminal graph materializers.
type PreflightContent struct {
	Configuration domain.ContentConfiguration
	TaskHealth    tournamentpreflight.TaskHealthInput
	TaskVersions  []capacity.TaskVersion
}

// LoadPreflightContent exposes the narrow content-only bridge needed by
// repositories that remain in the root postgres package.
func LoadPreflightContent(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (PreflightContent, error) {
	loaded, err := loadTournamentPreflightContent(ctx, querier, tournamentID)
	if err != nil {
		return PreflightContent{}, err
	}
	return PreflightContent{
		Configuration: loaded.configuration,
		TaskHealth:    loaded.taskHealth,
		TaskVersions:  loaded.taskVersions,
	}, nil
}

func loadTournamentPreflightContent(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (tournamentPreflightContent, error) {
	configuration, err := querier.GetCurrentTournamentContentConfiguration(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournamentPreflightContent{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - content configuration: %w",
			domain.ErrInvalidContentConfiguration,
		)
	}
	if err != nil {
		return tournamentPreflightContent{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - content configuration: %w", err,
		)
	}
	if configuration.ID == uuid.Nil || configuration.TournamentID != tournamentID || configuration.Revision < 1 {
		return tournamentPreflightContent{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - invalid content configuration: %w",
			domain.ErrInvalidContentConfiguration,
		)
	}
	categoryPools, err := loadTournamentPreflightCategoryPools(ctx, querier, configuration.ID)
	if err != nil {
		return tournamentPreflightContent{}, err
	}
	stageDefaults, err := loadTournamentPreflightStageDefaults(ctx, querier, configuration.ID)
	if err != nil {
		return tournamentPreflightContent{}, err
	}
	healthRows, err := querier.ListTaskPoolVersionHealth(
		ctx,
		[]uuid.UUID{configuration.NormalPoolRevisionID, configuration.GoldenPoolRevisionID},
	)
	if err != nil {
		return tournamentPreflightContent{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - task health: %w", err,
		)
	}
	normalPool, goldenPool, versions, taskVersions, err := tournamentPreflightTaskPools(configuration, healthRows)
	if err != nil {
		return tournamentPreflightContent{}, err
	}
	content, err := domain.CreateContentConfiguration(domain.ContentConfigurationInput{
		TournamentID:  tournamentID,
		ReserveCount:  int(configuration.ReserveCount),
		CategoryPools: categoryPools,
		NormalPool:    normalPool,
		GoldenPool:    goldenPool,
		StageDefaults: stageDefaults,
	})
	if err != nil {
		return tournamentPreflightContent{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - invalid published content: %w", err,
		)
	}
	content.Revision = configuration.Revision
	if err := content.Validate(); err != nil {
		return tournamentPreflightContent{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - invalid content revision: %w", err,
		)
	}
	return tournamentPreflightContent{
		configuration: content,
		taskHealth: tournamentpreflight.TaskHealthInput{
			NormalPool: content.NormalPool,
			GoldenPool: content.GoldenPool,
			Versions:   versions,
		},
		taskVersions: taskVersions,
	}, nil
}

func loadTournamentPreflightCategoryPools(
	ctx context.Context,
	querier *sqlc.Queries,
	configurationID uuid.UUID,
) ([]domain.CategoryPoolRevision, error) {
	rows, err := querier.ListTournamentContentCategoryPoolRevisions(ctx, configurationID)
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminRosterPostgres - LoadPreflightInput - category pools: %w", err)
	}
	poolsByID := make(map[uuid.UUID]*domain.CategoryPoolRevision, len(rows))
	for _, row := range rows {
		if row.ID == uuid.Nil || row.ConfigurationID != configurationID || row.Revision < 1 {
			return nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - invalid category pool: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		format := domain.SeriesFormat(row.Format)
		if !format.IsValid() {
			return nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - unknown category format: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		if _, exists := poolsByID[row.ID]; exists {
			return nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - duplicate category pool: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		poolsByID[row.ID] = &domain.CategoryPoolRevision{
			ID: row.ID, Revision: row.Revision, Format: format, Categories: []domain.Category{},
		}
	}
	memberships, err := querier.ListTournamentContentCategoryPoolMemberships(ctx, configurationID)
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminRosterPostgres - LoadPreflightInput - category memberships: %w", err)
	}
	for _, row := range memberships {
		pool, exists := poolsByID[row.CategoryPoolRevisionID]
		if !exists || !domain.Category(row.Category).IsValid() {
			return nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - invalid category membership: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		pool.Categories = append(pool.Categories, domain.Category(row.Category))
	}
	pools := make([]domain.CategoryPoolRevision, 0, len(poolsByID))
	for _, pool := range poolsByID {
		pools = append(pools, *pool)
	}
	return pools, nil
}

func loadTournamentPreflightStageDefaults(
	ctx context.Context,
	querier *sqlc.Queries,
	configurationID uuid.UUID,
) ([]domain.StageContentDefault, error) {
	rows, err := querier.ListTournamentContentStageDefaults(ctx, configurationID)
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminRosterPostgres - LoadPreflightInput - content defaults: %w", err)
	}
	defaults := make([]domain.StageContentDefault, 0, len(rows))
	for _, row := range rows {
		if row.ConfigurationID != configurationID {
			return nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - foreign content default: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		defaults = append(defaults, domain.StageContentDefault{
			Stage:                  domain.TournamentStage(row.Stage),
			Format:                 domain.SeriesFormat(row.Format),
			CategoryMode:           domain.CategoryMode(row.CategoryMode),
			CategoryPoolRevisionID: row.CategoryPoolRevisionID,
			TaskPoolKind:           domain.AssignmentTaskKind(row.TaskPoolKind),
		})
	}
	return defaults, nil
}

func tournamentPreflightTaskPools(
	configuration sqlc.GetCurrentTournamentContentConfigurationRow,
	rows []sqlc.ListTaskPoolVersionHealthRow,
) (
	domain.TaskPoolRevision,
	domain.TaskPoolRevision,
	[]domain.TaskVersionHealth,
	[]capacity.TaskVersion,
	error,
) {
	normal := domain.TaskPoolRevision{
		ID: configuration.NormalPoolRevisionID, Revision: configuration.NormalPoolRevision,
		Kind: domain.AssignmentTaskKindNormal, Versions: []domain.TaskVersionRef{},
	}
	golden := domain.TaskPoolRevision{
		ID: configuration.GoldenPoolRevisionID, Revision: configuration.GoldenPoolRevision,
		Kind: domain.AssignmentTaskKindGolden, Versions: []domain.TaskVersionRef{},
	}
	if normal.ID == uuid.Nil || normal.Revision < 1 || golden.ID == uuid.Nil || golden.Revision < 1 || normal.ID == golden.ID {
		return domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - invalid task pools: %w",
			domain.ErrInvalidContentConfiguration,
		)
	}
	versions := make([]domain.TaskVersionHealth, 0, len(rows))
	capacityVersions := make([]capacity.TaskVersion, 0, len(rows))
	seen := make(map[[2]uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		pool := &normal
		kind := domain.AssignmentTaskKindNormal
		if row.PoolRevisionID == golden.ID {
			pool = &golden
			kind = domain.AssignmentTaskKindGolden
		} else if row.PoolRevisionID != normal.ID {
			return domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - foreign task pool version: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		category := domain.Category(row.Category)
		if row.TaskID == uuid.Nil || row.TaskVersion < 1 ||
			domain.AssignmentTaskKind(row.PoolKind) != kind || !category.IsValid() {
			return domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - invalid task pool version: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		key := [2]uuid.UUID{row.TaskID, row.PoolRevisionID}
		if _, duplicate := seen[key]; duplicate {
			return domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - duplicate task pool version: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		seen[key] = struct{}{}
		pool.Versions = append(pool.Versions, domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)})
		versions = append(versions, domain.TaskVersionHealth{
			TaskID: row.TaskID, Version: int(row.TaskVersion), PoolRevisionID: row.PoolRevisionID,
			PoolKind: domain.AssignmentTaskKind(row.PoolKind), Exists: row.TaskExists,
			Enabled: row.TaskEnabled, Healthy: row.TaskHealthy, MutationLocked: row.TaskMutationLocked,
			PubliclyExposed: row.TaskPubliclyExposed,
		})
		capacityVersions = append(capacityVersions, capacity.TaskVersion{
			TaskVersionRef: domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)},
			PoolRevisionID: row.PoolRevisionID,
			PoolKind:       kind,
			Category:       category,
		})
	}
	return normal, golden, versions, capacityVersions, nil
}

func loadTournamentPreflightParticipants(
	ctx context.Context,
	querier *sqlc.Queries,
	authority rostercapability.RosterAuthority,
) ([]tournamentpreflight.Participant, error) {
	rows, err := querier.ListTournamentPreflightParticipants(
		ctx,
		sqlc.ListTournamentPreflightParticipantsParams{
			RosterID: authority.Roster.ID, TournamentID: authority.Roster.TournamentID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminRosterPostgres - LoadPreflightInput - participants: %w", err)
	}
	participants := make([]tournamentpreflight.Participant, len(rows))
	for index, row := range rows {
		participants[index] = tournamentpreflight.Participant{
			ParticipantID: row.ParticipantID, PlayerID: row.PlayerID, Seed: int(row.Seed),
			Attendance: domain.AttendanceState(row.Attendance), ReservedTournamentID: row.ReservedTournamentID,
		}
	}
	return participants, nil
}

func loadTournamentPreflightPairings(
	ctx context.Context,
	querier *sqlc.Queries,
	rosterID uuid.UUID,
	participants []tournamentpreflight.Participant,
	evaluatedAt time.Time,
) (int64, []swissusecase.Pair, []swissusecase.Pair, []tournamentpreflight.OverrideEvidence, uuid.UUID, error) {
	round, err := querier.GetTournamentPreflightRound(ctx, rosterID)
	if errors.Is(err, pgx.ErrNoRows) {
		pairings, byeID, planErr := plannedTournamentPreflightPairings(rosterID, participants, evaluatedAt)
		if planErr != nil {
			return 0, nil, nil, nil, uuid.Nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - planned pairings: %w", planErr,
			)
		}
		return 1, pairings, []swissusecase.Pair{}, []tournamentpreflight.OverrideEvidence{}, byeID, nil
	}
	if err != nil {
		return 0, nil, nil, nil, uuid.Nil, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - round: %w", err,
		)
	}
	rows, err := querier.ListTournamentPreflightPairings(
		ctx, sqlc.ListTournamentPreflightPairingsParams{RoundID: round.ID, RosterID: rosterID},
	)
	if err != nil {
		return 0, nil, nil, nil, uuid.Nil, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - pairings: %w", err,
		)
	}
	pairings := make([]swissusecase.Pair, 0, len(rows))
	repeated := make([]swissusecase.Pair, 0)
	overrides := make([]tournamentpreflight.OverrideEvidence, 0)
	for _, row := range rows {
		pair := swissusecase.Pair{
			FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
		}
		pairings = append(pairings, pair)
		if row.PriorMeetingCount > 0 {
			repeated = append(repeated, pair)
		}
		if row.OverrideActorID.Valid && row.OverrideReason != nil && row.OverrideConfirmedAt.Valid {
			overrides = append(overrides, tournamentpreflight.OverrideEvidence{
				Pair: pair, ActorID: row.OverrideActorID.UUID, Confirmed: true, Reason: *row.OverrideReason,
			})
		}
	}
	return round.Revision, pairings, repeated, overrides, round.ByeParticipantID.UUID, nil
}

func plannedTournamentPreflightPairings(
	rosterID uuid.UUID,
	participants []tournamentpreflight.Participant,
	evaluatedAt time.Time,
) ([]swissusecase.Pair, uuid.UUID, error) {
	participantIDs := make([]uuid.UUID, len(participants))
	for index, participant := range participants {
		participantIDs[index] = participant.ParticipantID
	}
	byeID := uuid.Nil
	if len(participantIDs)%2 != 0 {
		candidates := make([]swissusecase.ByeCandidate, len(participantIDs))
		for index, participantID := range participantIDs {
			candidates[index] = swissusecase.ByeCandidate{ParticipantID: participantID}
		}
		selection, err := swissusecase.SelectBye(
			uuid.NewSHA1(rosterID, []byte("preflight:bye-evidence")),
			uuid.NewSHA1(rosterID, []byte("preflight:round")),
			candidates,
			evaluatedAt,
		)
		if err != nil {
			return nil, uuid.Nil, err
		}
		byeID = selection.ParticipantID
		eligible := participantIDs[:0]
		for _, participantID := range participantIDs {
			if participantID != byeID {
				eligible = append(eligible, participantID)
			}
		}
		participantIDs = eligible
	}
	pairings, err := swissusecase.FindMatching(participantIDs, nil)
	if err != nil {
		return nil, uuid.Nil, err
	}
	return pairings, byeID, nil
}

func tournamentPreflightRuntime(
	authority rostercapability.RosterAuthority,
	evaluatedAt time.Time,
	rosterSize int,
	content domain.ContentConfiguration,
	taskVersions []capacity.TaskVersion,
) tournamentpreflight.RuntimeInput {
	storageRevision := "postgres:" + authority.ProjectionRevisionID.String() + "@" +
		strconv.FormatInt(authority.ProjectionRevision, 10)
	contentRevision := "content:" + content.TournamentID.String() + "@" +
		strconv.FormatInt(authority.ContentRevision, 10)
	storage := tournamentpreflight.ComponentHealth{Healthy: true, Revision: storageRevision}
	return tournamentpreflight.RuntimeInput{
		TournamentID: authority.Roster.TournamentID, Preset: authority.TournamentPreset,
		RosterSize: rosterSize, ContentRevision: authority.ContentRevision,
		Configuration: tournamentpreflight.ConfigurationHealth{Valid: true, Revision: contentRevision},
		Health: tournamentpreflight.Health{
			// This transaction has already read the authoritative store. Delivery
			// and realtime health are applied by the application before evaluation.
			AuthoritativeStorage: storage,
			Submission:           storage,
		},
		Capacity: tournamentPreflightCertification(authority, evaluatedAt, content, taskVersions),
		Clock:    tournamentpreflight.ClockHealth{ReferenceAt: evaluatedAt, MaxSkew: time.Second},
		Dependencies: []tournamentpreflight.DependencyHealth{
			{Name: tournamentpreflight.DependencyPostgres, Healthy: true, Revision: storageRevision},
		},
		Schedule: tournamentPreflightSchedule(authority.TournamentPreset, rosterSize, content, evaluatedAt),
	}
}

func tournamentPreflightCertification(
	authority rostercapability.RosterAuthority,
	certifiedAt time.Time,
	content domain.ContentConfiguration,
	taskVersions []capacity.TaskVersion,
) *tournamentpreflight.Certification {
	participantIDs := make([]uuid.UUID, len(authority.Roster.Participants))
	for index, participant := range authority.Roster.Participants {
		participantIDs[index] = participant.ID
	}
	normalVersions := taskVersionsForPool(taskVersions, domain.AssignmentTaskKindNormal)
	goldenVersions := taskVersionsForPool(taskVersions, domain.AssignmentTaskKindGolden)
	normal := capacity.ProveNormal(capacity.NormalInput{
		Preset: authority.TournamentPreset, ParticipantIDs: participantIDs,
		ReserveCount:  content.ReserveCount,
		CategoryPools: content.CategoryPools, NormalPool: content.NormalPool, Versions: normalVersions,
	})
	golden := capacity.ProveGolden(capacity.GoldenInput{
		Preset: authority.TournamentPreset, ParticipantIDs: participantIDs,
		ReserveCount: content.ReserveCount,
		NormalPool:   content.NormalPool, GoldenPool: content.GoldenPool, Versions: goldenVersions,
	})
	if !normal.Certified || !golden.Certified {
		return nil
	}
	certificateID := uuid.NewSHA1(
		authority.Roster.TournamentID,
		[]byte("capacity:"+normal.Digest+":"+golden.Digest),
	)
	certificate, err := tournamentpreflight.NewCertification(
		certificateID,
		authority.Roster.TournamentID,
		authority.ContentRevision,
		normal,
		golden,
		certifiedAt,
	)
	if err != nil {
		return nil
	}
	return &certificate
}

func taskVersionsForPool(
	versions []capacity.TaskVersion,
	kind domain.AssignmentTaskKind,
) []capacity.TaskVersion {
	result := make([]capacity.TaskVersion, 0, len(versions))
	for _, version := range versions {
		if version.PoolKind == kind {
			result = append(result, version)
		}
	}
	return result
}

func tournamentPreflightSchedule(
	preset domain.TournamentPreset,
	rosterSize int,
	content domain.ContentConfiguration,
	startsAt time.Time,
) tournamentpreflight.ScheduleHealth {
	rounds, err := preset.SwissRounds(rosterSize)
	if err != nil || !validServerTime(startsAt) {
		return tournamentpreflight.ScheduleHealth{}
	}
	stageGames := make(map[domain.TournamentStage]int, len(content.StageDefaults))
	for _, stageDefault := range content.StageDefaults {
		games := stageDefault.Format.WinsRequired()*2 - 1
		if games < 1 {
			return tournamentpreflight.ScheduleHealth{}
		}
		stageGames[stageDefault.Stage] = games
	}
	projectedGames := rounds*stageGames[domain.TournamentStageSwiss] +
		stageGames[domain.TournamentStageGolden] +
		stageGames[domain.TournamentStageSemifinal] +
		stageGames[domain.TournamentStageFinal]
	if projectedGames < 1 {
		return tournamentpreflight.ScheduleHealth{}
	}
	return tournamentpreflight.ScheduleHealth{
		StartsAt:          startsAt,
		MustFinishBy:      startsAt.Add(preset.NominalDuration()),
		ProjectedDuration: time.Duration(projectedGames) * preset.TaskDuration(),
	}
}
