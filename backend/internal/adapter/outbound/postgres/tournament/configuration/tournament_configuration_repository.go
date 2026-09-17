package configuration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
)

// TournamentConfigurationPostgres owns the SQL transaction for the
// configuration edit boundary. It deliberately does not call the existing
// execution repositories: configuration edits must either publish their
// command, lineage, and successor graph together or roll everything back.
type TournamentConfigurationPostgres struct {
	tx           *db.TxManager
	materializer SeriesMaterializer
}

func NewTournamentConfigurationPostgres(tx *db.TxManager) *TournamentConfigurationPostgres {
	return &TournamentConfigurationPostgres{tx: tx}
}

func NewTournamentConfigurationPostgresWithMaterializer(tx *db.TxManager, materializer SeriesMaterializer) *TournamentConfigurationPostgres {
	return &TournamentConfigurationPostgres{tx: tx, materializer: materializer}
}

var _ admin.TournamentConfigurationRepository = (*TournamentConfigurationPostgres)(nil)

func (r *TournamentConfigurationPostgres) LoadConfiguration(
	ctx context.Context,
	query admin.ConfigurationLoadQuery,
) (admin.ConfigurationAuthority, error) {
	if ctx == nil || r == nil || r.tx == nil || query.TournamentID == uuid.Nil || query.Operator.ActorID == uuid.Nil {
		return admin.ConfigurationAuthority{}, domain.ErrValidation
	}

	var authority admin.ConfigurationAuthority
	err := r.tx.ReadSnapshot(ctx, func(snapshotCtx context.Context) error {
		loaded, err := r.loadConfiguration(snapshotCtx, query.TournamentID, query.CommandID)
		if err != nil {
			return err
		}
		authority = loaded
		return nil
	})
	if err != nil {
		return admin.ConfigurationAuthority{}, fmt.Errorf("TournamentConfigurationPostgres - LoadConfiguration: %w", err)
	}
	return authority, nil
}

//nolint:gocyclo // One snapshot load validates and assembles the complete configuration authority.
func (r *TournamentConfigurationPostgres) loadConfiguration(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (admin.ConfigurationAuthority, error) {
	q := r.tx.Querier(ctx)
	roster, err := q.GetTournamentConfigurationEditRoster(ctx, tournamentID)
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select roster", err)
	}
	authorityRow, err := q.GetTournamentConfigurationEditAuthority(ctx, sqlc.GetTournamentConfigurationEditAuthorityParams{
		TournamentID: tournamentID, RosterID: roster.ID,
	})
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select authority", err)
	}
	standingsRow, err := q.GetTournamentConfigurationEditPublishedStandings(ctx, sqlc.GetTournamentConfigurationEditPublishedStandingsParams{
		TournamentID: tournamentID, RosterID: roster.ID,
		ProjectionRevisionID: authorityRow.ProjectionRevisionID, ExpectedProjectionRevision: authorityRow.ProjectionRevision,
	})
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select published standings", err)
	}
	participantRows, err := q.ListTournamentConfigurationEditParticipants(ctx, roster.ID)
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select participants", err)
	}
	participants := make([]pairingusecase.PairingParticipant, 0, len(participantRows))
	for _, row := range participantRows {
		attendance := domain.AttendanceState(row.Attendance)
		if !attendance.IsValid() {
			return admin.ConfigurationAuthority{}, domain.ErrInternal
		}
		if attendance != domain.AttendanceStateCheckedIn {
			continue
		}
		participants = append(participants, pairingusecase.PairingParticipant{ID: row.ID, StableSeed: int(row.Seed)})
	}
	standings, err := tournamentAdminStandings(standingsRow, participants)
	if err != nil {
		return admin.ConfigurationAuthority{}, fmt.Errorf("decode published standings: %w", err)
	}
	configuration, err := q.GetTournamentConfigurationEditConfiguration(ctx, sqlc.GetTournamentConfigurationEditConfigurationParams{
		TournamentID: tournamentID, ConfigurationID: authorityRow.ConfigurationID,
	})
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select configuration", err)
	}
	pools, err := q.ListTournamentConfigurationEditPoolRevisions(ctx, configuration.ID)
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select category pools", err)
	}
	memberships, err := q.ListTournamentConfigurationEditPoolMemberships(ctx, configuration.ID)
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select category memberships", err)
	}
	defaults, err := q.ListTournamentConfigurationEditStageDefaults(ctx, configuration.ID)
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select stage defaults", err)
	}
	taskVersions, err := q.ListTaskPoolVersionHealth(ctx, []uuid.UUID{
		configuration.NormalPoolRevisionID,
		configuration.GoldenPoolRevisionID,
	})
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select task pool versions", err)
	}
	seriesRows, err := q.ListActiveTournamentConfigurationEditSeries(ctx, sqlc.ListActiveTournamentConfigurationEditSeriesParams{
		TournamentID: tournamentID, RosterID: roster.ID,
	})
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select active Series", err)
	}
	stageRows, err := q.ListTournamentConfigurationEditSeriesStageBindings(ctx, sqlc.ListTournamentConfigurationEditSeriesStageBindingsParams{
		TournamentID: tournamentID, RosterID: roster.ID,
	})
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select Series stage bindings", err)
	}
	roundRows, err := q.ListTournamentConfigurationEditSwissRounds(ctx, sqlc.ListTournamentConfigurationEditSwissRoundsParams{
		TournamentID: tournamentID, RosterID: roster.ID,
	})
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select Swiss rounds", err)
	}
	reservationRows, err := q.ListTournamentConfigurationEditReservations(ctx, sqlc.ListTournamentConfigurationEditReservationsParams{
		TournamentID: tournamentID, RosterID: roster.ID,
	})
	if err != nil {
		return admin.ConfigurationAuthority{}, configurationQueryError("select reservations", err)
	}

	content, poolByID, err := configurationContent(configuration, pools, memberships, defaults, taskVersions)
	if err != nil {
		return admin.ConfigurationAuthority{}, err
	}
	stageDefaults, stageDefaultsRows, err := configurationStageDefaults(content, defaults, poolByID)
	if err != nil {
		return admin.ConfigurationAuthority{}, err
	}
	content.StageDefaults = stageDefaultsRows
	stageBySeries := make(map[uuid.UUID]struct {
		stage domain.TournamentStage
		round int
	}, len(stageRows))
	for _, row := range stageRows {
		stage := domain.TournamentStage(row.Stage)
		if !stage.IsValid() {
			return admin.ConfigurationAuthority{}, fmt.Errorf("invalid Series stage %q: %w", row.Stage, domain.ErrInvalidContentConfiguration)
		}
		stageBySeries[row.ID] = struct {
			stage domain.TournamentStage
			round int
		}{stage: stage, round: int(row.RoundNumber)}
	}
	reservationsBySeries := make(map[uuid.UUID][]admin.ConfigurationReservation)
	for _, row := range reservationRows {
		seriesID := row.SeriesID
		if !seriesID.Valid {
			seriesID = row.AssignmentSeriesID
		}
		if !seriesID.Valid {
			continue
		}
		if len(row.SelectionEvidence) == 0 {
			return admin.ConfigurationAuthority{}, domain.ErrInternal
		}
		reservationsBySeries[seriesID.UUID] = append(reservationsBySeries[seriesID.UUID], admin.ConfigurationReservation{
			ID: row.ID, OwnerID: row.OwnerID, SourceRevisionID: row.SourceRevisionID, Revision: row.Revision,
			Used: row.State == "committed", Disclosed: row.DisclosedAt.Valid,
			EvidenceDigest: sha256.Sum256(row.SelectionEvidence),
		})
	}

	series := make([]admin.ConfigurationSeries, 0, len(seriesRows))
	seriesByID := make(map[uuid.UUID]admin.ConfigurationSeries, len(seriesRows))
	artifacts := make([]admin.ConfigurationArtifact, 0, len(seriesRows)+len(roundRows))
	for _, row := range seriesRows {
		binding, ok := stageBySeries[row.ID]
		if !ok {
			binding = struct {
				stage domain.TournamentStage
				round int
			}{stage: domain.TournamentStageGolden}
		}
		mode, categories, err := seriesCategories(row, binding.stage, stageDefaults, poolByID)
		if err != nil {
			return admin.ConfigurationAuthority{}, err
		}
		pool := poolForSeries(content, row.Format)
		item := admin.ConfigurationSeries{
			ID: row.ID, FirstParticipantID: row.FirstParticipantID, SecondParticipantID: row.SecondParticipantID,
			Stage: binding.stage, RoundNumber: binding.round, Revision: row.Revision,
			Mode: mode, Categories: categories, CategoryPoolRevisionID: pool.ID, CategoryPoolRevision: pool.Revision,
			State: domain.SeriesState(row.State), Locked: row.State != string(domain.SeriesStatePlanned),
			Started: row.StartedAt.Valid, Consumed: seriesConsumed(row.State),
			Disclosed: seriesDisclosed(reservationsBySeries[row.ID]), Reservations: cloneConfigurationReservations(reservationsBySeries[row.ID]),
		}
		series = append(series, item)
		seriesByID[item.ID] = item
		artifacts = append(artifacts, configurationSeriesArtifact(item))
	}

	rounds := make([]admin.ConfigurationRound, 0, len(roundRows))
	for _, row := range roundRows {
		participants, err := decodeConfigurationUUIDArray(row.ParticipantIds)
		if err != nil {
			return admin.ConfigurationAuthority{}, fmt.Errorf("decode Swiss participants: %w", err)
		}
		seriesIDs, err := decodeConfigurationUUIDArray(row.SeriesIds)
		if err != nil {
			return admin.ConfigurationAuthority{}, fmt.Errorf("decode Swiss Series: %w", err)
		}
		reservations := make([]admin.ConfigurationReservation, 0)
		for _, seriesID := range seriesIDs {
			reservations = append(reservations, reservationsBySeries[seriesID]...)
		}
		item := admin.ConfigurationRound{
			ID: row.ID, Stage: domain.TournamentStageSwiss, Number: int(row.RoundNumber), Revision: row.Revision,
			ParticipantIDs: participants, SeriesIDs: seriesIDs, Locked: row.LockedAt.Valid || row.LockRevision != nil,
			ByeParticipantID: configurationUUIDPointer(row.ByeParticipantID), ByeRevisionID: configurationUUIDPointer(row.ByeRevisionID),
			Started: row.LockedAt.Valid, Consumed: row.LockedAt.Valid, Disclosed: seriesDisclosed(reservations), Reservations: cloneConfigurationReservations(reservations),
		}
		for _, seriesID := range seriesIDs {
			if bound, ok := seriesByID[seriesID]; ok {
				item.Pairings = append(item.Pairings, inbound.AdminConfigurationParticipantPair{
					FirstParticipantID: bound.FirstParticipantID, SecondParticipantID: bound.SecondParticipantID,
				})
			}
		}
		rounds = append(rounds, item)
		artifacts = append(artifacts, configurationRoundArtifact(item))
	}

	authority := admin.ConfigurationAuthority{
		TournamentID: tournamentID, ProjectionRevisionID: authorityRow.ProjectionRevisionID, ProjectionRevision: authorityRow.ProjectionRevision,
		Configuration: content, TournamentState: domain.TournamentState(authorityRow.TournamentState), TournamentRevision: authorityRow.TournamentRevision,
		SwissDefault: stageDefaults[domain.TournamentStageSwiss], GoldenDefault: stageDefaults[domain.TournamentStageGolden],
		SemifinalDefault: stageDefaults[domain.TournamentStageSemifinal], FinalDefault: stageDefaults[domain.TournamentStageFinal],
		Series: series, Rounds: rounds, Standings: standings, Artifacts: artifacts, UpdatedAt: configurationTime(configuration.PublishedAt, configurationTime(configuration.CreatedAt, time.Unix(0, 0).UTC())),
	}
	if commandID != uuid.Nil {
		if command, commandErr := q.GetTournamentConfigurationEditCommand(ctx, sqlc.GetTournamentConfigurationEditCommandParams{
			TournamentID: tournamentID, CommandID: commandID,
		}); commandErr == nil {
			authority.Recorded = configurationCommandRecord(command)
		} else if !errors.Is(commandErr, pgx.ErrNoRows) {
			return admin.ConfigurationAuthority{}, configurationQueryError("select command replay", commandErr)
		}
	}
	return authority, nil
}

//nolint:gocyclo // One transaction preserves publication, rebuild, invalidation and audit atomicity.
func (r *TournamentConfigurationPostgres) ExecuteMutation(
	ctx context.Context,
	mutation admin.ConfigurationMutation,
) (admin.ConfigurationMutationResult, error) {
	if ctx == nil || r == nil || r.tx == nil || mutation.CommandID == uuid.Nil || mutation.Authority.TournamentID == uuid.Nil {
		return admin.ConfigurationMutationResult{}, domain.ErrValidation
	}
	var evidence inbound.AdminConfigurationMutationEvidence
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		q := r.tx.Querier(txCtx)
		observedAt, err := q.ReadTournamentExecutionTime(txCtx)
		if err != nil {
			return configurationQueryError("read mutation time", err)
		}
		if !observedAt.Valid || !domain.IsValidServerTime(observedAt.Time.UTC()) {
			return domain.ErrInternal
		}
		mutation.Evidence.RequestedAt = observedAt.Time.UTC()
		roster, err := q.GetTournamentConfigurationEditRoster(txCtx, mutation.Authority.TournamentID)
		if err != nil {
			return configurationQueryError("lock roster", err)
		}
		current, err := q.GetTournamentConfigurationEditAuthority(txCtx, sqlc.GetTournamentConfigurationEditAuthorityParams{
			TournamentID: mutation.Authority.TournamentID, RosterID: roster.ID,
		})
		if err != nil {
			return configurationQueryError("read mutation cutoff", err)
		}
		locked, err := q.LockTournamentConfigurationEditAuthority(txCtx, sqlc.LockTournamentConfigurationEditAuthorityParams{
			TournamentID: mutation.Authority.TournamentID, RosterID: roster.ID,
			ExpectedConfigurationID: current.ConfigurationID, ExpectedConfigurationRevision: mutation.Authority.Configuration.Revision,
			ExpectedConfigurationHeadRevision: current.ConfigurationHeadRevision,
			ExpectedProjectionRevisionID:      mutation.Authority.ProjectionRevisionID, ExpectedProjectionRevision: mutation.Authority.ProjectionRevision,
			ExpectedCutoffID: current.CutoffID, ExpectedCutoffSequence: current.CutoffSequence,
		})
		if err != nil || locked.ConfigurationRevision != mutation.Authority.Configuration.Revision || locked.ProjectionRevision != mutation.Authority.ProjectionRevision {
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return configurationQueryError("lock mutation authority", err)
			}
			return fmt.Errorf("lock mutation authority projection %s/%d vs %s/%d, configuration %d vs %d: %w",
				mutation.Authority.ProjectionRevisionID, mutation.Authority.ProjectionRevision,
				current.ProjectionRevisionID, current.ProjectionRevision,
				mutation.Authority.Configuration.Revision, current.ConfigurationRevision, domain.ErrConflict)
		}
		currentConfiguration, err := q.GetTournamentConfigurationEditConfiguration(txCtx, sqlc.GetTournamentConfigurationEditConfigurationParams{
			TournamentID: mutation.Authority.TournamentID, ConfigurationID: current.ConfigurationID,
		})
		if err != nil {
			return configurationQueryError("read current configuration", err)
		}

		resultConfigurationID := current.ConfigurationID
		resultConfigurationRevision := current.ConfigurationRevision
		if mutation.Operation == "update_configuration" {
			resultConfigurationID = uuid.New()
			resultConfigurationRevision = mutation.NextConfiguration.Revision
			if _, err := q.CreateTournamentConfigurationEditDraft(txCtx, sqlc.CreateTournamentConfigurationEditDraftParams{
				ConfigurationID: resultConfigurationID, TournamentID: mutation.Authority.TournamentID,
				SourceConfigurationID: current.ConfigurationID, SourceConfigurationRevision: current.ConfigurationRevision,
				SourceConfigurationHeadRevision: current.ConfigurationHeadRevision,
				PoolPublicationID:               currentConfiguration.PoolPublicationID,
				NormalPoolRevisionID:            mutation.NextConfiguration.NormalPool.ID, GoldenPoolRevisionID: mutation.NextConfiguration.GoldenPool.ID,
				CreatedAt: validTimestamp(mutation.Evidence.RequestedAt),
			}); err != nil {
				return configurationQueryError("create configuration draft", err)
			}
			if err := r.insertConfigurationChildren(txCtx, q, mutation, resultConfigurationID); err != nil {
				return err
			}
			if _, err := q.PublishTournamentConfigurationEditDraftCAS(txCtx, sqlc.PublishTournamentConfigurationEditDraftCASParams{
				ConfigurationID: resultConfigurationID, TournamentID: mutation.Authority.TournamentID,
				SourceConfigurationID: current.ConfigurationID, SourceConfigurationRevision: current.ConfigurationRevision,
				SourceConfigurationHeadRevision: current.ConfigurationHeadRevision,
				RosterID:                        roster.ID, ExpectedProjectionRevisionID: mutation.Authority.ProjectionRevisionID,
				ExpectedProjectionRevision: mutation.Authority.ProjectionRevision, ExpectedCutoffID: current.CutoffID,
				ExpectedCutoffSequence: current.CutoffSequence, PublishedAt: validTimestamp(mutation.Evidence.RequestedAt),
			}); err != nil {
				return configurationQueryError("publish configuration draft", err)
			}
		}

		if err := r.releaseConfigurationReservations(txCtx, q, mutation, roster.ID); err != nil {
			return err
		}
		if err := r.applyRoundChange(txCtx, q, mutation, resultConfigurationID, resultConfigurationRevision, roster.ID); err != nil {
			return err
		}
		if err := r.applySeriesChanges(txCtx, q, mutation, resultConfigurationID, resultConfigurationRevision, roster.ID); err != nil {
			return err
		}
		command, err := q.CreateTournamentConfigurationEditCommand(txCtx, r.commandParams(mutation, roster.ID, current, resultConfigurationID, resultConfigurationRevision))
		if err != nil {
			return configurationQueryError("record configuration edit command", err)
		}
		if err := r.applyUnlockIntents(txCtx, q, mutation, roster.ID); err != nil {
			return err
		}
		if err := r.recordLineage(txCtx, q, mutation, command.CommandID, roster.ID, resultConfigurationID, resultConfigurationRevision, validTimestamp(mutation.Evidence.RequestedAt)); err != nil {
			return err
		}
		evidence = mutation.Evidence
		return nil
	})
	if err != nil {
		return admin.ConfigurationMutationResult{}, fmt.Errorf("TournamentConfigurationPostgres - ExecuteMutation: %w", err)
	}
	return admin.ConfigurationMutationResult{Evidence: evidence, Changed: true}, nil
}

func (r *TournamentConfigurationPostgres) insertConfigurationChildren(
	ctx context.Context,
	q *sqlc.Queries,
	mutation admin.ConfigurationMutation,
	configurationID uuid.UUID,
) error {
	at := validTimestamp(mutation.Evidence.RequestedAt)
	poolIDs := make(map[uuid.UUID]uuid.UUID, len(mutation.NextConfiguration.CategoryPools))
	for _, pool := range mutation.NextConfiguration.CategoryPools {
		poolID := uuid.New()
		poolIDs[pool.ID] = poolID
		if _, err := q.CreateTournamentConfigurationEditPoolRevision(ctx, sqlc.CreateTournamentConfigurationEditPoolRevisionParams{
			PoolRevisionID: poolID, ConfigurationID: configurationID, Format: string(pool.Format), Revision: pool.Revision, CreatedAt: at,
		}); err != nil {
			return configurationQueryError("create category pool", err)
		}
		for _, category := range pool.Categories {
			if _, err := q.CreateTournamentConfigurationEditPoolMembership(ctx, sqlc.CreateTournamentConfigurationEditPoolMembershipParams{
				PoolRevisionID: poolID, Category: string(category), CreatedAt: at,
			}); err != nil {
				return configurationQueryError("create category membership", err)
			}
		}
	}
	defaults := mutation.NextConfiguration.StageDefaults
	for _, stage := range []domain.TournamentStage{domain.TournamentStageSwiss, domain.TournamentStageGolden, domain.TournamentStageSemifinal, domain.TournamentStageFinal} {
		var selected domain.StageContentDefault
		for _, value := range defaults {
			if value.Stage == stage {
				selected = value
				break
			}
		}
		categories := mutationStageCategories(mutation, stage, selected.CategoryPoolRevisionID)
		categoryPoolID := poolIDs[selected.CategoryPoolRevisionID]
		if categoryPoolID == uuid.Nil {
			return fmt.Errorf("stage %s references an unknown category pool: %w", stage, domain.ErrInvalidContentConfiguration)
		}
		if _, err := q.CreateTournamentConfigurationEditStageDefault(ctx, sqlc.CreateTournamentConfigurationEditStageDefaultParams{
			ConfigurationID: configurationID, Stage: string(selected.Stage), Format: string(selected.Format), CategoryMode: string(selected.CategoryMode),
			CategoryPoolRevisionID: categoryPoolID, TaskPoolKind: string(selected.TaskPoolKind), Categories: categoriesJSON(categories), CreatedAt: at,
		}); err != nil {
			return configurationQueryError("create stage default", err)
		}
	}
	return nil
}

func (r *TournamentConfigurationPostgres) applySeriesChanges(
	ctx context.Context,
	q *sqlc.Queries,
	mutation admin.ConfigurationMutation,
	configurationID uuid.UUID,
	configurationRevision int64,
	rosterID uuid.UUID,
) error {
	changes := make([]admin.ConfigurationSeriesChange, 0, 1)
	if mutation.SeriesChange != nil {
		changes = append(changes, *mutation.SeriesChange)
	}
	if mutation.RoundChange != nil {
		changes = append(changes, mutation.RoundChange.Series...)
	}
	for _, change := range changes {
		successorID := successorIDFor(mutation.Rebuilt, change.Previous.ID)
		if successorID == uuid.Nil {
			return fmt.Errorf("missing Series successor for %s: %w", change.Previous.ID, domain.ErrInternal)
		}
		wave, err := q.GetTournamentConfigurationEditSeriesWave(ctx, sqlc.GetTournamentConfigurationEditSeriesWaveParams{
			SeriesID: change.Previous.ID, RosterID: rosterID, TournamentID: mutation.Authority.TournamentID,
		})
		if err != nil {
			return configurationQueryError("lock Series Wave", err)
		}
		initialScoreID := uuid.NewSHA1(mutation.CommandID, []byte("configuration-series-score:"+successorID.String()))
		categories := categoriesJSON(change.Next.Categories)
		if _, err := q.CreateTournamentConfigurationEditSeriesSuccessor(ctx, sqlc.CreateTournamentConfigurationEditSeriesSuccessorParams{
			SuccessorSeriesID: successorID, SuccessorState: "planned", ConfigurationID: configurationID,
			ConfigurationRevision: configurationRevision, CategoryMode: string(change.Next.Mode), EffectiveCategories: categories,
			InitialScoreRevisionID: nullUUID(uuid.Nil), CreatedAt: validTimestamp(mutation.Evidence.RequestedAt), SourceSeriesID: change.Previous.ID, TournamentID: mutation.Authority.TournamentID,
			FirstParticipantID: change.Next.FirstParticipantID, SecondParticipantID: change.Next.SecondParticipantID,
			RosterID: rosterID, ExpectedSeriesRevision: change.Previous.Revision,
		}); err != nil {
			return configurationQueryError("create Series successor", err)
		}
		if err := r.createConfigurationSeriesGenesis(ctx, q, mutation, wave.ID, successorID, initialScoreID, change.Next, rosterID); err != nil {
			return err
		}
		if _, err := q.SupersedeTournamentConfigurationEditSeriesCAS(ctx, sqlc.SupersedeTournamentConfigurationEditSeriesCASParams{
			SuccessorSeriesID: successorID, SupersededAt: validTimestamp(mutation.Evidence.RequestedAt), Reason: mutation.Evidence.Reason,
			SourceSeriesID: change.Previous.ID, TournamentID: mutation.Authority.TournamentID, RosterID: rosterID, ExpectedSeriesRevision: change.Previous.Revision,
		}); err != nil {
			return configurationQueryError("supersede Series", err)
		}
		if _, err := q.DeleteTournamentConfigurationEditSupersededWaveSeriesCAS(
			ctx,
			sqlc.DeleteTournamentConfigurationEditSupersededWaveSeriesCASParams{
				WaveID: wave.ID, SourceSeriesID: change.Previous.ID, SuccessorSeriesID: successorID,
				TournamentID: mutation.Authority.TournamentID, RosterID: rosterID,
			},
		); err != nil {
			return configurationQueryError("detach superseded Series from Wave", err)
		}
	}
	return nil
}

func (r *TournamentConfigurationPostgres) createConfigurationSeriesGenesis(
	ctx context.Context,
	q *sqlc.Queries,
	mutation admin.ConfigurationMutation,
	waveID, seriesID, initialScoreID uuid.UUID,
	series admin.ConfigurationSeries,
	rosterID uuid.UUID,
) error {
	at := validTimestamp(mutation.Evidence.RequestedAt)
	if err := q.CreateInitialSeriesScoreRevision(ctx, sqlc.CreateInitialSeriesScoreRevisionParams{
		ID: initialScoreID, TournamentID: mutation.Authority.TournamentID, RosterID: rosterID, SeriesID: seriesID,
		CommandID: mutation.CommandID, SourceProjectionRevisionID: mutation.Authority.ProjectionRevisionID,
		SourceProjectionRevision: mutation.Authority.ProjectionRevision, CreatedAt: at,
	}); err != nil {
		return configurationQueryError("create successor score revision", err)
	}
	if _, err := q.CreateInitialSeriesScoreHead(ctx, sqlc.CreateInitialSeriesScoreHeadParams{
		SeriesID: seriesID, RosterID: rosterID, InitialScoreRevisionID: initialScoreID, UpdatedAt: at,
	}); err != nil {
		return configurationQueryError("create successor score head", err)
	}
	if err := q.CreateWaveSeries(ctx, sqlc.CreateWaveSeriesParams{
		WaveID: waveID, TournamentID: mutation.Authority.TournamentID, RosterID: rosterID,
		SeriesID: seriesID, CreatedAt: at,
	}); err != nil {
		return configurationQueryError("attach successor Series", err)
	}
	if r.materializer == nil {
		return domain.ErrInternal
	}
	if err := r.materializer.CreateGenesis(ctx, q, SeriesGenesisInput{
		WaveID: waveID, SeriesID: seriesID, InitialScoreRevisionID: initialScoreID,
		TournamentID: mutation.Authority.TournamentID, RosterID: rosterID, CommandID: mutation.CommandID,
		SourceProjectionRevisionID: mutation.Authority.ProjectionRevisionID,
		SourceProjectionRevision:   mutation.Authority.ProjectionRevision,
		CreatedAt:                  mutation.Evidence.RequestedAt,
		FirstParticipantID:         series.FirstParticipantID, SecondParticipantID: series.SecondParticipantID,
	}); err != nil {
		return err
	}
	assignmentCommandID := uuid.NewSHA1(mutation.CommandID, []byte("configuration-series-assignment:"+seriesID.String()))
	plan := pairingusecase.PairingPlan{
		Command: pairingusecase.PairingCommand{CommandScope: admin.CommandScope{Operator: admin.OperatorIdentity{ActorID: mutation.Evidence.OperatorID},
			TournamentID: mutation.Authority.TournamentID, CommandID: assignmentCommandID}, CategoryMode: series.Mode, Categories: append([]domain.Category(nil), series.Categories...)},
		Authority: pairingusecase.PairingAuthority{RosterID: rosterID}, SeriesIDs: []uuid.UUID{seriesID},
		Pairs:     []swissusecase.Pair{{FirstParticipantID: series.FirstParticipantID, SecondParticipantID: series.SecondParticipantID}},
		DecidedAt: mutation.Evidence.RequestedAt,
	}
	switch series.Mode {
	case domain.CategoryModeRandom, domain.CategoryModeAdmin, domain.CategoryModeDraft:
		if err := r.materializer.Materialize(ctx, plan); err != nil {
			return fmt.Errorf("materialize %s successor: %w", series.Mode, err)
		}
	default:
		return domain.ErrValidation
	}
	return nil
}

//nolint:gocyclo // One transaction keeps historical pairing checks and the rebuilt round graph atomic.
func (r *TournamentConfigurationPostgres) applyRoundChange(
	ctx context.Context,
	q *sqlc.Queries,
	mutation admin.ConfigurationMutation,
	configurationID uuid.UUID,
	configurationRevision int64,
	rosterID uuid.UUID,
) error {
	if mutation.RoundChange == nil {
		return nil
	}
	priorMeetingCounts, err := mutation.Authority.PriorMeetingCountsBeforeRound(mutation.RoundChange.Previous.Number)
	if err != nil {
		return err
	}
	if !maps.Equal(priorMeetingCounts, mutation.RoundChange.PriorMeetingCounts) {
		return fmt.Errorf("swiss prior meeting evidence does not match authority: %w", domain.ErrValidation)
	}
	if err := q.DeleteSwissOpponentHistory(ctx, mutation.RoundChange.Previous.ID); err != nil {
		return configurationQueryError("delete superseded Swiss opponent history", err)
	}
	if err := q.DeleteSwissPairingMembers(ctx, mutation.RoundChange.Previous.ID); err != nil {
		return configurationQueryError("delete superseded Swiss pairing members", err)
	}
	if err := q.DeleteSwissPairings(ctx, mutation.RoundChange.Previous.ID); err != nil {
		return configurationQueryError("delete superseded Swiss pairings", err)
	}
	if err := q.DeleteSwissRepeatOverride(ctx, mutation.RoundChange.Previous.ID); err != nil {
		return configurationQueryError("delete superseded Swiss repeat override", err)
	}
	previousByeParticipantID := derefUUID(mutation.RoundChange.Previous.ByeParticipantID)
	previousByeRevisionID := derefUUID(mutation.RoundChange.Previous.ByeRevisionID)
	nextByeParticipantID := derefUUID(mutation.RoundChange.Next.ByeParticipantID)
	nextByeRevisionID := derefUUID(mutation.RoundChange.Next.ByeRevisionID)
	if (previousByeParticipantID == uuid.Nil) != (previousByeRevisionID == uuid.Nil) ||
		(nextByeParticipantID == uuid.Nil) != (nextByeRevisionID == uuid.Nil) {
		return fmt.Errorf("swiss bye participant and revision must be both present or both absent: %w", domain.ErrValidation)
	}
	if mutation.RoundChange.Bye == nil {
		if nextByeParticipantID != uuid.Nil || nextByeRevisionID != uuid.Nil {
			return fmt.Errorf("swiss bye selection is missing for a non-null bye link: %w", domain.ErrValidation)
		}
	} else if mutation.RoundChange.Bye.ParticipantID != nextByeParticipantID || mutation.RoundChange.Bye.Evidence.ID != nextByeRevisionID {
		return fmt.Errorf("swiss bye link does not match selection evidence: %w", domain.ErrValidation)
	}
	if err := q.DeleteSwissBye(ctx, mutation.RoundChange.Previous.ID); err != nil {
		return configurationQueryError("delete superseded Swiss bye", err)
	}
	if mutation.RoundChange.Bye != nil {
		bye, err := configurationByeParams(configurationSwissRoundMeta{
			ID: mutation.RoundChange.Previous.ID, RosterID: rosterID, RecordedAt: mutation.Evidence.RequestedAt,
		}, *mutation.RoundChange.Bye)
		if err != nil {
			return fmt.Errorf("prepare revised Swiss bye: %w", err)
		}
		if err := q.CreateSwissBye(ctx, bye); err != nil {
			return configurationQueryError("create revised Swiss bye", err)
		}
	}
	if _, err := q.UpdateTournamentConfigurationEditSwissWaveLinkByeCAS(ctx, sqlc.UpdateTournamentConfigurationEditSwissWaveLinkByeCASParams{
		TournamentID: mutation.Authority.TournamentID, RosterID: rosterID, RoundID: mutation.RoundChange.Previous.ID,
		ExpectedRoundRevision:    mutation.RoundChange.Previous.Revision,
		ExpectedByeParticipantID: nullUUID(previousByeParticipantID), ExpectedByeRevisionID: nullUUID(previousByeRevisionID),
		NextByeParticipantID: nullUUID(nextByeParticipantID), NextByeRevisionID: nullUUID(nextByeRevisionID),
	}); err != nil {
		return configurationQueryError("update Swiss bye link", err)
	}
	for index, pair := range mutation.RoundChange.Next.Pairings {
		key := swissusecase.NewPairKey(pair.FirstParticipantID, pair.SecondParticipantID)
		priorMeetingCount := priorMeetingCounts[key]
		if priorMeetingCount < 0 || priorMeetingCount > math.MaxInt16 {
			return fmt.Errorf("invalid prior Swiss meeting count for pairing %d: %w", index+1, domain.ErrValidation)
		}
		if priorMeetingCount > 0 {
			return fmt.Errorf("repeated Swiss pairing cannot be persisted by configuration edit: %w", domain.ErrValidation)
		}
		pairingID := uuid.NewSHA1(mutation.CommandID, []byte(fmt.Sprintf("configuration-round-pairing:%d", index+1)))
		if err := q.CreateSwissPairing(ctx, sqlc.CreateSwissPairingParams{ID: pairingID, RoundID: mutation.RoundChange.Previous.ID,
			RosterID: rosterID, SlotNumber: int16(index + 1), CreatedAt: validTimestamp(mutation.Evidence.RequestedAt)}); err != nil {
			return configurationQueryError("create revised Swiss pairing", err)
		}
		for seat, participantID := range []uuid.UUID{pair.FirstParticipantID, pair.SecondParticipantID} {
			if err := q.CreateSwissPairingMember(ctx, sqlc.CreateSwissPairingMemberParams{PairingID: pairingID,
				RoundID: mutation.RoundChange.Previous.ID, RosterID: rosterID, Seat: int16(seat + 1),
				ParticipantID: participantID, CreatedAt: validTimestamp(mutation.Evidence.RequestedAt)}); err != nil {
				return configurationQueryError("create revised Swiss pairing member", err)
			}
		}
		if err := q.CreateSwissOpponentHistory(ctx, sqlc.CreateSwissOpponentHistoryParams{PairingID: pairingID,
			RoundID: mutation.RoundChange.Previous.ID, RosterID: rosterID, PriorMeetingCount: int16(priorMeetingCount),
			RecordedAt: validTimestamp(mutation.Evidence.RequestedAt)}); err != nil {
			return configurationQueryError("create revised Swiss opponent history", err)
		}
	}
	pairingInputs, err := json.Marshal(mutation.RoundChange.Next.Pairings)
	if err != nil {
		return fmt.Errorf("marshal Swiss pairing inputs: %w", err)
	}
	mode := mutation.Authority.SwissDefault.Mode
	categories := mutation.Authority.SwissDefault.Categories
	if len(mutation.RoundChange.Series) > 0 {
		mode = mutation.RoundChange.Series[0].Next.Mode
		categories = mutation.RoundChange.Series[0].Next.Categories
	}
	if _, err := q.UpdateTournamentConfigurationEditSwissRoundCAS(ctx, sqlc.UpdateTournamentConfigurationEditSwissRoundCASParams{
		RoundID: mutation.RoundChange.Previous.ID, TournamentID: mutation.Authority.TournamentID, RosterID: rosterID,
		ExpectedRoundRevision: mutation.RoundChange.Previous.Revision, PairingInputs: pairingInputs,
		ConfigurationID: configurationID, ConfigurationRevision: configurationRevision, CategoryMode: string(mode),
		EffectiveCategories: categoriesJSON(categories), UpdatedAt: validTimestamp(mutation.Evidence.RequestedAt),
	}); err != nil {
		return configurationQueryError("update Swiss round", err)
	}
	return nil
}

func (r *TournamentConfigurationPostgres) applyUnlockIntents(ctx context.Context, q *sqlc.Queries, mutation admin.ConfigurationMutation, rosterID uuid.UUID) error {
	for _, artifact := range mutation.Affected {
		ids := make([]uuid.UUID, 0, len(artifact.Reservations))
		for _, reservation := range artifact.Reservations {
			ids = append(ids, reservation.ID)
		}
		kind := artifact.Kind
		if kind == "round" {
			kind = "swiss_round"
		}
		if kind != "series" && kind != "swiss_round" {
			continue
		}
		if _, err := q.CreateTournamentConfigurationEditUnlockIntent(ctx, sqlc.CreateTournamentConfigurationEditUnlockIntentParams{
			IntentID: uuid.New(), CommandID: mutation.CommandID, TournamentID: mutation.Authority.TournamentID, RosterID: rosterID,
			ArtifactKind: kind, ArtifactID: artifact.ID, ExpectedRevision: artifact.Revision, ReservationIds: uniqueUUIDs(ids),
			InvalidateProof: true, InvalidateReadiness: true, CreatedAt: validTimestamp(mutation.Evidence.RequestedAt),
		}); err != nil {
			return configurationQueryError("record unlock intent", err)
		}
	}
	return nil
}

func (r *TournamentConfigurationPostgres) releaseConfigurationReservations(
	ctx context.Context,
	q *sqlc.Queries,
	mutation admin.ConfigurationMutation,
	rosterID uuid.UUID,
) error {
	reservationIDs := make([]uuid.UUID, 0)
	for _, artifact := range mutation.Affected {
		for _, reservation := range artifact.Reservations {
			reservationIDs = append(reservationIDs, reservation.ID)
		}
	}
	reservationIDs = uniqueUUIDs(reservationIDs)
	if len(reservationIDs) == 0 {
		return nil
	}
	locked, err := q.LockTournamentConfigurationEditReservations(ctx, sqlc.LockTournamentConfigurationEditReservationsParams{
		TournamentID: mutation.Authority.TournamentID, RosterID: rosterID, ReservationIds: reservationIDs,
	})
	if err != nil {
		return configurationQueryError("lock unlock reservations", err)
	}
	if len(locked) != len(reservationIDs) {
		return fmt.Errorf("lock %d of %d reservations: %w", len(locked), len(reservationIDs), domain.ErrConflict)
	}
	released, err := q.ReleaseTournamentConfigurationEditReservations(ctx, sqlc.ReleaseTournamentConfigurationEditReservationsParams{
		TournamentID: mutation.Authority.TournamentID, RosterID: rosterID, ReservationIds: reservationIDs,
		OccurredAt: validTimestamp(mutation.Evidence.RequestedAt), Reason: mutation.Evidence.Reason,
	})
	if err != nil {
		return configurationQueryError("release unlock reservations", err)
	}
	if len(released) != len(reservationIDs) {
		return fmt.Errorf("release %d of %d reservations: %w", len(released), len(reservationIDs), domain.ErrConflict)
	}
	return nil
}

func (r *TournamentConfigurationPostgres) recordLineage(
	ctx context.Context,
	q *sqlc.Queries,
	mutation admin.ConfigurationMutation,
	commandID uuid.UUID,
	rosterID uuid.UUID,
	configurationID uuid.UUID,
	configurationRevision int64,
	at pgtype.Timestamptz,
) error {
	lineageDocument, err := json.Marshal(map[string]any{"operation": mutation.Operation, "configuration_id": configurationID, "configuration_revision": configurationRevision})
	if err != nil {
		return fmt.Errorf("marshal configuration lineage: %w", err)
	}
	for _, artifact := range mutation.Affected {
		kind := artifact.Kind
		if kind == "round" {
			kind = "swiss_round"
		}
		successor := successorIDFor(mutation.Rebuilt, artifact.ID)
		successorRevision := artifact.Revision + 1
		if _, err := q.CreateTournamentConfigurationEditArtifactLineage(ctx, sqlc.CreateTournamentConfigurationEditArtifactLineageParams{
			LineageID: uuid.New(), CommandID: commandID, TournamentID: mutation.Authority.TournamentID, RosterID: rosterID,
			ArtifactKind: kind, SourceArtifactID: artifact.ID, SourceArtifactRevision: &artifact.Revision,
			SuccessorArtifactID: nullUUID(successor), SuccessorArtifactRevision: &successorRevision, LineageDocument: lineageDocument,
			SupersededAt: at, CreatedAt: at,
		}); err != nil {
			return configurationQueryError("record artifact lineage", err)
		}
		for _, invalidationKind := range []string{"proof", "readiness"} {
			if _, err := q.CreateTournamentConfigurationEditInvalidation(ctx, sqlc.CreateTournamentConfigurationEditInvalidationParams{
				InvalidationID: uuid.New(), CommandID: commandID, TournamentID: mutation.Authority.TournamentID, RosterID: rosterID,
				ArtifactKind: invalidationKind, ArtifactID: artifact.ID, ArtifactRevision: &artifact.Revision, Reason: mutation.Evidence.Reason, CreatedAt: at,
			}); err != nil {
				return configurationQueryError("record artifact invalidation", err)
			}
		}
	}
	return nil
}

func (r *TournamentConfigurationPostgres) commandParams(
	mutation admin.ConfigurationMutation,
	rosterID uuid.UUID,
	current sqlc.GetTournamentConfigurationEditAuthorityRow,
	resultConfigurationID uuid.UUID,
	resultConfigurationRevision int64,
) sqlc.CreateTournamentConfigurationEditCommandParams {
	action := map[string]string{"update_configuration": "defaults", "update_unstarted_series": "series", "revise_swiss_round": "swiss_round"}[mutation.Operation]
	var sourceSeriesID, resultSeriesID, sourceRoundID *uuid.UUID
	var sourceSeriesRevision, resultSeriesRevision, sourceRoundRevision, resultRoundRevision *int64
	if mutation.SeriesChange != nil {
		sourceSeriesID = ptrUUID(mutation.SeriesChange.Previous.ID)
		result := successorIDFor(mutation.Rebuilt, mutation.SeriesChange.Previous.ID)
		resultSeriesID = ptrUUID(result)
		sourceSeriesRevision = ptrInt64(mutation.SeriesChange.Previous.Revision)
		resultSeriesRevision = ptrInt64(1)
	}
	if mutation.RoundChange != nil {
		sourceRoundID = ptrUUID(mutation.RoundChange.Previous.ID)
		sourceRoundRevision = ptrInt64(mutation.RoundChange.Previous.Revision)
		resultRoundRevision = ptrInt64(mutation.RoundChange.Next.Revision)
	}
	resultDocument, _ := json.Marshal(mutation.Evidence)
	intentDocument, _ := json.Marshal(map[string]any{"operation": mutation.Operation, "reason": mutation.Evidence.Reason, "unlock_intents": len(mutation.UnlockIntents)})
	digest := mutation.RequestDigest
	return sqlc.CreateTournamentConfigurationEditCommandParams{
		CommandID: mutation.CommandID, TournamentID: mutation.Authority.TournamentID, RosterID: rosterID, ActorID: mutation.Evidence.OperatorID,
		Action: action, SourceProjectionRevisionID: mutation.Authority.ProjectionRevisionID, SourceProjectionRevision: mutation.Authority.ProjectionRevision,
		SourceCutoffID: current.CutoffID, SourceCutoffSequence: current.CutoffSequence,
		SourceConfigurationID: current.ConfigurationID, SourceConfigurationRevision: current.ConfigurationRevision,
		ResultConfigurationID: resultConfigurationID, ResultConfigurationRevision: resultConfigurationRevision,
		SourceTournamentRevision: mutation.Authority.TournamentRevision, ResultTournamentRevision: mutation.Authority.TournamentRevision,
		SourceSeriesID: nullUUID(derefUUID(sourceSeriesID)), SourceSeriesRevision: sourceSeriesRevision,
		ResultSeriesID: nullUUID(derefUUID(resultSeriesID)), ResultSeriesRevision: resultSeriesRevision,
		SourceRoundID: nullUUID(derefUUID(sourceRoundID)), SourceRoundRevision: sourceRoundRevision, ResultRoundRevision: resultRoundRevision,
		RequestDigest: digest[:], IntentDocument: intentDocument, ResultDocument: resultDocument,
		OccurredAt: validTimestamp(mutation.Evidence.RequestedAt), CreatedAt: validTimestamp(mutation.Evidence.RequestedAt),
	}
}

func configurationQueryError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("TournamentConfigurationPostgres - %s: %w", operation, domain.ErrConflict)
	}
	return fmt.Errorf("TournamentConfigurationPostgres - %s: %w", operation, err)
}

func configurationContent(
	row sqlc.GetTournamentConfigurationEditConfigurationRow,
	pools []sqlc.TournamentCategoryPoolRevision,
	memberships []sqlc.TournamentCategoryPoolMembership,
	_ []sqlc.ListTournamentConfigurationEditStageDefaultsRow,
	taskVersions []sqlc.ListTaskPoolVersionHealthRow,
) (domain.ContentConfiguration, map[uuid.UUID]domain.CategoryPoolRevision, error) {
	byID := make(map[uuid.UUID]domain.CategoryPoolRevision, len(pools))
	for _, pool := range pools {
		byID[pool.ID] = domain.CategoryPoolRevision{ID: pool.ID, Revision: pool.Revision, Format: domain.SeriesFormat(pool.Format), Categories: []domain.Category{}}
	}
	for _, membership := range memberships {
		pool, ok := byID[membership.CategoryPoolRevisionID]
		if !ok {
			return domain.ContentConfiguration{}, nil, domain.ErrInvalidContentConfiguration
		}
		category := domain.Category(membership.Category)
		if !category.IsValid() {
			return domain.ContentConfiguration{}, nil, domain.ErrInvalidContentConfiguration
		}
		pool.Categories = append(pool.Categories, category)
		byID[membership.CategoryPoolRevisionID] = pool
	}
	categoryPools := make([]domain.CategoryPoolRevision, 0, len(byID))
	for _, pool := range byID {
		sort.Slice(pool.Categories, func(i, j int) bool { return pool.Categories[i] < pool.Categories[j] })
		categoryPools = append(categoryPools, pool)
	}
	sort.Slice(categoryPools, func(i, j int) bool { return categoryPools[i].Format < categoryPools[j].Format })
	normalVersions := make([]domain.TaskVersionRef, 0)
	goldenVersions := make([]domain.TaskVersionRef, 0)
	for _, item := range taskVersions {
		version := domain.TaskVersionRef{TaskID: item.TaskID, Version: int(item.TaskVersion)}
		switch item.PoolRevisionID {
		case row.NormalPoolRevisionID:
			normalVersions = append(normalVersions, version)
		case row.GoldenPoolRevisionID:
			goldenVersions = append(goldenVersions, version)
		default:
			return domain.ContentConfiguration{}, nil, domain.ErrInvalidContentConfiguration
		}
	}
	slices.SortFunc(normalVersions, domain.CompareTaskVersionRefs)
	slices.SortFunc(goldenVersions, domain.CompareTaskVersionRefs)
	return domain.ContentConfiguration{
		TournamentID: row.TournamentID, Revision: row.Revision, CategoryPools: categoryPools,
		NormalPool:    domain.TaskPoolRevision{ID: row.NormalPoolRevisionID, Revision: row.NormalPoolRevision, Kind: domain.AssignmentTaskKindNormal, Versions: normalVersions},
		GoldenPool:    domain.TaskPoolRevision{ID: row.GoldenPoolRevisionID, Revision: row.GoldenPoolRevision, Kind: domain.AssignmentTaskKindGolden, Versions: goldenVersions},
		StageDefaults: []domain.StageContentDefault{},
	}, byID, nil
}

func configurationStageDefaults(
	content domain.ContentConfiguration,
	rows []sqlc.ListTournamentConfigurationEditStageDefaultsRow,
	pools map[uuid.UUID]domain.CategoryPoolRevision,
) (map[domain.TournamentStage]admin.ConfigurationStageDefault, []domain.StageContentDefault, error) {
	result := make(map[domain.TournamentStage]admin.ConfigurationStageDefault, len(rows))
	content.StageDefaults = make([]domain.StageContentDefault, 0, len(rows))
	for _, row := range rows {
		stage := domain.TournamentStage(row.Stage)
		mode := domain.CategoryMode(row.CategoryMode)
		pool, ok := pools[row.CategoryPoolRevisionID]
		if !ok || !stage.IsValid() || !mode.IsValid() {
			return nil, nil, domain.ErrInvalidContentConfiguration
		}
		categories, err := decodeConfigurationCategories(row.Categories)
		if err != nil {
			return nil, nil, err
		}
		categories = fallbackCategories(mode, categories, pool.Categories)
		result[stage] = admin.ConfigurationStageDefault{Mode: mode, Categories: categories, CategoryPoolRevisionID: pool.ID, CategoryPoolRevision: pool.Revision}
		content.StageDefaults = append(content.StageDefaults, domain.StageContentDefault{Stage: stage, Format: domain.SeriesFormat(row.Format), CategoryMode: mode, CategoryPoolRevisionID: pool.ID, TaskPoolKind: domain.AssignmentTaskKind(row.TaskPoolKind)})
	}
	return result, content.StageDefaults, nil
}

func seriesCategories(row sqlc.ListActiveTournamentConfigurationEditSeriesRow, stage domain.TournamentStage, defaults map[domain.TournamentStage]admin.ConfigurationStageDefault, pools map[uuid.UUID]domain.CategoryPoolRevision) (domain.CategoryMode, []domain.Category, error) {
	if row.CategoryMode != nil {
		categories, err := decodeConfigurationCategories(row.EffectiveCategories)
		if err != nil {
			return "", nil, err
		}
		if len(categories) > 0 {
			return domain.CategoryMode(*row.CategoryMode), categories, nil
		}
	}
	selected := defaults[stage]
	pool := poolForFormat(pools, domain.SeriesFormat(row.Format))
	if selected.CategoryPoolRevisionID != uuid.Nil {
		pool = pools[selected.CategoryPoolRevisionID]
	}
	return selected.Mode, fallbackCategories(selected.Mode, selected.Categories, pool.Categories), nil
}

func poolForSeries(content domain.ContentConfiguration, format string) domain.CategoryPoolRevision {
	return poolForFormatFromSlice(content.CategoryPools, domain.SeriesFormat(format))
}

func poolForFormat(pools map[uuid.UUID]domain.CategoryPoolRevision, format domain.SeriesFormat) domain.CategoryPoolRevision {
	for _, pool := range pools {
		if pool.Format == format {
			return pool
		}
	}
	return domain.CategoryPoolRevision{}
}

func poolForFormatFromSlice(pools []domain.CategoryPoolRevision, format domain.SeriesFormat) domain.CategoryPoolRevision {
	for _, pool := range pools {
		if pool.Format == format {
			return pool
		}
	}
	return domain.CategoryPoolRevision{}
}

func fallbackCategories(mode domain.CategoryMode, categories, pool []domain.Category) []domain.Category {
	if len(categories) > 0 {
		return append([]domain.Category(nil), categories...)
	}
	if mode == domain.CategoryModeDraft {
		return append([]domain.Category(nil), pool...)
	}
	if len(pool) > 0 {
		return []domain.Category{pool[0]}
	}
	return nil
}

func decodeConfigurationCategories(raw []byte) ([]domain.Category, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("decode categories: %w", err)
	}
	result := make([]domain.Category, len(values))
	for i, value := range values {
		result[i] = domain.Category(value)
		if !result[i].IsValid() {
			return nil, domain.ErrInvalidContentConfiguration
		}
	}
	return result, nil
}

func decodeConfigurationUUIDArray(raw any) ([]uuid.UUID, error) {
	var encoded []byte
	switch value := raw.(type) {
	case nil:
		return []uuid.UUID{}, nil
	case []byte:
		encoded = value
	case string:
		encoded = []byte(value)
	default:
		var err error
		encoded, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	if len(encoded) == 0 || string(encoded) == "null" {
		return []uuid.UUID{}, nil
	}
	var values []string
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, err
	}
	result := make([]uuid.UUID, len(values))
	for i, value := range values {
		parsed, err := uuid.Parse(value)
		if err != nil {
			return nil, err
		}
		result[i] = parsed
	}
	return result, nil
}

func configurationCommandRecord(row sqlc.TournamentConfigurationEditCommand) *admin.ConfigurationCommandRecord {
	var evidence inbound.AdminConfigurationMutationEvidence
	_ = json.Unmarshal(row.ResultDocument, &evidence)
	var digest [sha256.Size]byte
	copy(digest[:], row.RequestDigest)
	operation := map[string]string{
		"defaults":    "update_configuration",
		"series":      "update_unstarted_series",
		"swiss_round": "revise_swiss_round",
	}[row.Action]
	return &admin.ConfigurationCommandRecord{CommandID: row.CommandID, TournamentID: row.TournamentID, OperatorID: row.ActorID, Operation: operation,
		ExpectedProjectionRevision: row.SourceProjectionRevision, ExpectedConfigurationRevision: row.SourceConfigurationRevision,
		ExpectedSeriesRevision: derefInt64(row.SourceSeriesRevision), ExpectedRoundRevision: derefInt64(row.SourceRoundRevision),
		RequestDigest: digest, Evidence: evidence, ExecutedAt: configurationTime(row.OccurredAt, time.Unix(0, 0).UTC())}
}

func configurationSeriesArtifact(value admin.ConfigurationSeries) admin.ConfigurationArtifact {
	return admin.ConfigurationArtifact{Kind: "series", ID: value.ID, Stage: value.Stage, Revision: value.Revision, SeriesID: value.ID, RoundNumber: value.RoundNumber,
		State: string(value.State), Locked: value.Locked, Started: value.Started, Consumed: value.Consumed, Disclosed: value.Disclosed, Reservations: cloneConfigurationReservations(value.Reservations)}
}

func configurationRoundArtifact(value admin.ConfigurationRound) admin.ConfigurationArtifact {
	return admin.ConfigurationArtifact{Kind: "round", ID: value.ID, Stage: value.Stage, Revision: value.Revision, RoundNumber: value.Number,
		Locked: value.Locked, Started: value.Started, Consumed: value.Consumed, Disclosed: value.Disclosed, Reservations: cloneConfigurationReservations(value.Reservations)}
}

func cloneConfigurationReservations(values []admin.ConfigurationReservation) []admin.ConfigurationReservation {
	return append([]admin.ConfigurationReservation(nil), values...)
}

func seriesConsumed(state string) bool {
	return state == "active" || state == "replay_required" || state == "technical_pause" || state == "completed" || state == "cancelled"
}

func seriesDisclosed(values []admin.ConfigurationReservation) bool {
	for _, value := range values {
		if value.Disclosed {
			return true
		}
	}
	return false
}

func categoriesJSON(values []domain.Category) []byte {
	data, _ := json.Marshal(values)
	return data
}

func mutationStageCategories(mutation admin.ConfigurationMutation, stage domain.TournamentStage, poolID uuid.UUID) []domain.Category {
	switch stage {
	case domain.TournamentStageSwiss:
		return append([]domain.Category(nil), mutation.NextSwissDefault.Categories...)
	case domain.TournamentStageSemifinal:
		return append([]domain.Category(nil), mutation.NextSemifinalDefault.Categories...)
	case domain.TournamentStageFinal:
		for _, pool := range mutation.NextConfiguration.CategoryPools {
			if pool.ID == poolID {
				return append([]domain.Category(nil), pool.Categories...)
			}
		}
	case domain.TournamentStageGolden:
		for _, pool := range mutation.NextConfiguration.CategoryPools {
			if pool.ID == poolID && len(pool.Categories) > 0 {
				return []domain.Category{pool.Categories[0]}
			}
		}
	}
	for _, pool := range mutation.NextConfiguration.CategoryPools {
		if pool.ID == poolID {
			if len(pool.Categories) > 0 {
				return []domain.Category{pool.Categories[0]}
			}
		}
	}
	return nil
}

func successorIDFor(rebuilt []admin.ConfigurationArtifact, source uuid.UUID) uuid.UUID {
	for _, artifact := range rebuilt {
		if artifact.PreviousRevisionID == source {
			return artifact.ID
		}
	}
	return uuid.Nil
}

func uniqueUUIDs(values []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(values))
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func configurationTime(value pgtype.Timestamptz, fallback time.Time) time.Time {
	if value.Valid {
		return value.Time.Round(0).UTC()
	}
	return fallback
}

func validTimestamp(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return pgtype.Timestamptz{Time: value.Round(0).UTC(), Valid: true}
}

func ptrUUID(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	return &value
}

func derefUUID(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
}

func ptrInt64(value int64) *int64 { return &value }

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func nullUUID(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func configurationUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	result := value.UUID
	return &result
}
