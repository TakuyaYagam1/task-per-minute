package postgres

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
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func (r *TournamentAdminRosterPostgres) LoadPreflightInput(
	ctx context.Context,
	authority tournamentadmin.RosterAuthority,
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
	content, taskHealth, err := loadTournamentPreflightContent(ctx, querier, authority.Roster.TournamentID)
	if err != nil {
		return tournamentpreflight.ReportInput{}, err
	}
	pairingRevision, pairings, repeated, overrides, byeID, err := loadTournamentPreflightPairings(
		ctx, querier, authority.Roster.ID,
	)
	if err != nil {
		return tournamentpreflight.ReportInput{}, err
	}
	rosterSize := len(participants)
	return tournamentpreflight.ReportInput{
		RosterRevision: authority.Roster.Revision, PairingRevision: pairingRevision,
		Structural: tournamentpreflight.StructuralInput{
			TournamentID: authority.Roster.TournamentID, Preset: authority.TournamentPreset,
			ExpectedRosterSize: rosterSize, Participants: participants,
			CategoryPools: content.CategoryPools,
			Pairings:      pairings, ByeParticipantID: byeID,
			RepeatedPairings: repeated, Overrides: overrides,
		},
		TaskHealth: taskHealth,
		Runtime:    tournamentPreflightRuntime(authority, evaluatedAt, rosterSize, content),
	}, nil
}

func loadTournamentPreflightContent(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (domain.ContentConfiguration, tournamentpreflight.TaskHealthInput, error) {
	configuration, err := querier.GetCurrentTournamentContentConfiguration(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ContentConfiguration{}, tournamentpreflight.TaskHealthInput{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - content configuration: %w",
			domain.ErrInvalidContentConfiguration,
		)
	}
	if err != nil {
		return domain.ContentConfiguration{}, tournamentpreflight.TaskHealthInput{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - content configuration: %w", err,
		)
	}
	if configuration.ID == uuid.Nil || configuration.TournamentID != tournamentID || configuration.Revision < 1 {
		return domain.ContentConfiguration{}, tournamentpreflight.TaskHealthInput{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - invalid content configuration: %w",
			domain.ErrInvalidContentConfiguration,
		)
	}
	categoryPools, err := loadTournamentPreflightCategoryPools(ctx, querier, configuration.ID)
	if err != nil {
		return domain.ContentConfiguration{}, tournamentpreflight.TaskHealthInput{}, err
	}
	stageDefaults, err := loadTournamentPreflightStageDefaults(ctx, querier, configuration.ID)
	if err != nil {
		return domain.ContentConfiguration{}, tournamentpreflight.TaskHealthInput{}, err
	}
	healthRows, err := querier.ListTaskPoolVersionHealth(
		ctx,
		[]uuid.UUID{configuration.NormalPoolRevisionID, configuration.GoldenPoolRevisionID},
	)
	if err != nil {
		return domain.ContentConfiguration{}, tournamentpreflight.TaskHealthInput{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - task health: %w", err,
		)
	}
	normalPool, goldenPool, versions, err := tournamentPreflightTaskPools(configuration, healthRows)
	if err != nil {
		return domain.ContentConfiguration{}, tournamentpreflight.TaskHealthInput{}, err
	}
	content, err := domain.CreateContentConfiguration(domain.ContentConfigurationInput{
		TournamentID:  tournamentID,
		CategoryPools: categoryPools,
		NormalPool:    normalPool,
		GoldenPool:    goldenPool,
		StageDefaults: stageDefaults,
	})
	if err != nil {
		return domain.ContentConfiguration{}, tournamentpreflight.TaskHealthInput{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - invalid published content: %w", err,
		)
	}
	content.Revision = configuration.Revision
	if err := content.Validate(); err != nil {
		return domain.ContentConfiguration{}, tournamentpreflight.TaskHealthInput{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - invalid content revision: %w", err,
		)
	}
	return content, tournamentpreflight.TaskHealthInput{
		NormalPool: content.NormalPool,
		GoldenPool: content.GoldenPool,
		Versions:   versions,
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
) (domain.TaskPoolRevision, domain.TaskPoolRevision, []domain.TaskVersionHealth, error) {
	normal := domain.TaskPoolRevision{
		ID: configuration.NormalPoolRevisionID, Revision: configuration.NormalPoolRevision,
		Kind: domain.AssignmentTaskKindNormal, Versions: []domain.TaskVersionRef{},
	}
	golden := domain.TaskPoolRevision{
		ID: configuration.GoldenPoolRevisionID, Revision: configuration.GoldenPoolRevision,
		Kind: domain.AssignmentTaskKindGolden, Versions: []domain.TaskVersionRef{},
	}
	if normal.ID == uuid.Nil || normal.Revision < 1 || golden.ID == uuid.Nil || golden.Revision < 1 || normal.ID == golden.ID {
		return domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, fmt.Errorf(
			"TournamentAdminRosterPostgres - LoadPreflightInput - invalid task pools: %w",
			domain.ErrInvalidContentConfiguration,
		)
	}
	versions := make([]domain.TaskVersionHealth, 0, len(rows))
	seen := make(map[[2]uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		pool := &normal
		kind := domain.AssignmentTaskKindNormal
		if row.PoolRevisionID == golden.ID {
			pool = &golden
			kind = domain.AssignmentTaskKindGolden
		} else if row.PoolRevisionID != normal.ID {
			return domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - foreign task pool version: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		if row.TaskID == uuid.Nil || row.TaskVersion < 1 || domain.AssignmentTaskKind(row.PoolKind) != kind {
			return domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, fmt.Errorf(
				"TournamentAdminRosterPostgres - LoadPreflightInput - invalid task pool version: %w",
				domain.ErrInvalidContentConfiguration,
			)
		}
		key := [2]uuid.UUID{row.TaskID, row.PoolRevisionID}
		if _, duplicate := seen[key]; duplicate {
			return domain.TaskPoolRevision{}, domain.TaskPoolRevision{}, nil, fmt.Errorf(
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
	}
	return normal, golden, versions, nil
}

func loadTournamentPreflightParticipants(
	ctx context.Context,
	querier *sqlc.Queries,
	authority tournamentadmin.RosterAuthority,
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
) (int64, []swissusecase.Pair, []swissusecase.Pair, []tournamentpreflight.OverrideEvidence, uuid.UUID, error) {
	round, err := querier.GetTournamentPreflightRound(ctx, rosterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 1, []swissusecase.Pair{}, []swissusecase.Pair{}, []tournamentpreflight.OverrideEvidence{}, uuid.Nil, nil
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

func tournamentPreflightRuntime(
	authority tournamentadmin.RosterAuthority,
	evaluatedAt time.Time,
	rosterSize int,
	content domain.ContentConfiguration,
) tournamentpreflight.RuntimeInput {
	storageRevision := "postgres:" + authority.ProjectionRevisionID.String() + "@" +
		strconv.FormatInt(authority.ProjectionRevision, 10)
	contentRevision := "content:" + content.TournamentID.String() + "@" + strconv.FormatInt(content.Revision, 10)
	storage := tournamentpreflight.ComponentHealth{Healthy: true, Revision: storageRevision}
	return tournamentpreflight.RuntimeInput{
		TournamentID: authority.Roster.TournamentID, Preset: authority.TournamentPreset,
		RosterSize: rosterSize, ContentRevision: content.Revision,
		Configuration: tournamentpreflight.ConfigurationHealth{Valid: true, Revision: contentRevision},
		Health: tournamentpreflight.Health{
			// This transaction has already read the authoritative store. Delivery
			// and realtime health are applied by the application before evaluation.
			AuthoritativeStorage: storage,
			Submission:           storage,
		},
		Clock: tournamentpreflight.ClockHealth{ReferenceAt: evaluatedAt, MaxSkew: time.Second},
		Dependencies: []tournamentpreflight.DependencyHealth{
			{Name: tournamentpreflight.DependencyPostgres, Healthy: true, Revision: storageRevision},
		},
	}
}
