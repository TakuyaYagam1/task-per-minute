package postgres

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrWaveNotFound = errors.New("wave repository: wave not found")

type WavePostgres struct {
	tx *TxManager
}

type WaveCreateInput struct {
	ID                         uuid.UUID
	TournamentID               uuid.UUID
	RosterID                   uuid.UUID
	RevisionID                 domain.WaveRevisionID
	ReplacesWaveID             *uuid.UUID
	ParticipantIDs             []uuid.UUID
	Series                     []WaveSeriesInput
	CommandID                  uuid.UUID
	SourceProjectionRevisionID uuid.UUID
	SourceProjectionRevision   int64
	CreatedAt                  time.Time
}

type WaveSeriesInput struct {
	ID                     uuid.UUID
	FirstParticipantID     uuid.UUID
	SecondParticipantID    uuid.UUID
	Format                 domain.SeriesFormat
	InitialScoreRevisionID domain.SeriesScoreRevisionID
}

type ReadyWindowInput struct {
	ID         uuid.UUID
	RevisionID domain.ReadyWindowRevisionID
	OpenedAt   time.Time
	Deadline   time.Time
}

type WaveRecord struct {
	Wave               domain.Wave
	RosterID           uuid.UUID
	ReplacesWaveID     *uuid.UUID
	Revision           int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ClosedAt           *time.Time
	ReadyAt            map[uuid.UUID]time.Time
	ReadinessRevisions map[uuid.UUID]int64
	Series             []domain.Series
}

func NewWavePostgres(tx *TxManager) *WavePostgres {
	return &WavePostgres{tx: tx}
}

func (r *WavePostgres) Create(
	ctx context.Context,
	in WaveCreateInput,
) (*WaveRecord, error) {
	if err := validateWaveCreateInput(in); err != nil {
		return nil, err
	}
	participantIDs := append([]uuid.UUID(nil), in.ParticipantIDs...)
	sort.Slice(participantIDs, func(i, j int) bool {
		return participantIDs[i].String() < participantIDs[j].String()
	})
	series := append([]WaveSeriesInput(nil), in.Series...)
	sort.Slice(series, func(i, j int) bool {
		return series[i].ID.String() < series[j].ID.String()
	})
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.CreateWave(txCtx, sqlc.CreateWaveParams{
			ID:             in.ID,
			TournamentID:   in.TournamentID,
			RosterID:       in.RosterID,
			RevisionID:     in.RevisionID.UUID(),
			ReplacesWaveID: nullableUUID(in.ReplacesWaveID),
			CreatedAt:      tstz(in.CreatedAt),
		}); err != nil {
			return err
		}
		for _, participantID := range participantIDs {
			if err := querier.CreateWaveMember(txCtx, sqlc.CreateWaveMemberParams{
				WaveID:        in.ID,
				RosterID:      in.RosterID,
				ParticipantID: participantID,
				CreatedAt:     tstz(in.CreatedAt),
			}); err != nil {
				return err
			}
			if _, err := querier.CreateWaveReadinessHead(txCtx, sqlc.CreateWaveReadinessHeadParams{
				WaveID: in.ID, RosterID: in.RosterID, ParticipantID: participantID,
				CreatedAt: tstz(in.CreatedAt),
			}); err != nil {
				return err
			}
		}
		for _, item := range series {
			if _, err := querier.CreateSeries(txCtx, sqlc.CreateSeriesParams{
				ID: item.ID, TournamentID: in.TournamentID, RosterID: in.RosterID,
				FirstParticipantID:  item.FirstParticipantID,
				SecondParticipantID: item.SecondParticipantID,
				Format:              string(item.Format), CreatedAt: tstz(in.CreatedAt),
			}); err != nil {
				return err
			}
			if err := querier.CreateInitialSeriesScoreRevision(txCtx, sqlc.CreateInitialSeriesScoreRevisionParams{
				ID: item.InitialScoreRevisionID.UUID(), TournamentID: in.TournamentID, RosterID: in.RosterID,
				SeriesID: item.ID, CommandID: in.CommandID, SourceProjectionRevisionID: in.SourceProjectionRevisionID,
				SourceProjectionRevision: in.SourceProjectionRevision, CreatedAt: tstz(in.CreatedAt),
			}); err != nil {
				return err
			}
			if _, err := querier.CreateInitialSeriesScoreHead(txCtx, sqlc.CreateInitialSeriesScoreHeadParams{
				SeriesID: item.ID, RosterID: in.RosterID, InitialScoreRevisionID: item.InitialScoreRevisionID.UUID(),
				UpdatedAt: tstz(in.CreatedAt),
			}); err != nil {
				return err
			}
			if err := querier.CreateWaveSeries(txCtx, sqlc.CreateWaveSeriesParams{
				WaveID: in.ID, TournamentID: in.TournamentID, RosterID: in.RosterID,
				SeriesID: item.ID, CreatedAt: tstz(in.CreatedAt),
			}); err != nil {
				return err
			}
			if err := createWaveGenesisProjectionNode(txCtx, querier, in, item); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, mapRepositoryWriteError("WavePostgres - Create", err)
	}
	return r.Get(ctx, in.TournamentID, in.ID)
}

func createWaveGenesisProjectionNode(
	ctx context.Context,
	querier *sqlc.Queries,
	wave WaveCreateInput,
	series WaveSeriesInput,
) error {
	authorityID := resultProjectionWaveAuthorityID(wave.ID)
	if err := querier.CreateResultProjectionNodeAuthority(ctx, sqlc.CreateResultProjectionNodeAuthorityParams{
		ID: authorityID, TournamentID: wave.TournamentID, RosterID: wave.RosterID,
		SourceKind: "wave_initialization", WaveID: nullableUUIDValue(wave.ID), CreatedAt: tstz(wave.CreatedAt),
	}); err != nil {
		return mapRepositoryWriteError("WavePostgres - Create - create score provenance", err)
	}
	payload, err := marshalJSON("WavePostgres - Create - score genesis projection node", map[string]any{
		"schema": "result-projection-series-score-genesis-v1", "wave_id": wave.ID.String(),
		"series_id": series.ID.String(), "revision_id": series.InitialScoreRevisionID.UUID().String(),
		"first_wins": 0, "second_wins": 0,
	})
	if err != nil {
		return err
	}
	return createResultProjectionNode(ctx, querier, sqlc.CreateResultProjectionNodeParams{
		ID: series.InitialScoreRevisionID.UUID(), AuthorityID: authorityID, TournamentID: wave.TournamentID,
		RosterID: wave.RosterID, ArtifactKind: string(domain.ArtifactKindSeriesScore), EntityID: series.ID,
		RevisionNumber: 1, Payload: payload, PayloadDigest: digestBytes(payload), CreatedAt: tstz(wave.CreatedAt),
	})
}

func resultProjectionWaveAuthorityID(waveID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(waveID, []byte("result-projection-wave-initialization"))
}

func (r *WavePostgres) OpenReadyWindow(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedRevision int64,
	in ReadyWindowInput,
) (*WaveRecord, bool, error) {
	if !validOpenReadyWindowCommand(tournamentID, waveID, expectedRevision, in) {
		return nil, false, domain.ErrValidation
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.GetWave(txCtx, sqlc.GetWaveParams{ID: waveID, TournamentID: tournamentID})
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision || current.State != string(domain.WaveStatePlanned) {
			return errWaveCAS
		}
		if _, err = querier.CreateReadyWindow(txCtx, sqlc.CreateReadyWindowParams{
			ID:         in.ID,
			WaveID:     waveID,
			RosterID:   current.RosterID,
			RevisionID: in.RevisionID.UUID(),
			OpenedAt:   tstz(in.OpenedAt),
			Deadline:   tstz(in.Deadline),
		}); err != nil {
			return err
		}
		heads, err := querier.BindWaveReadinessHeads(txCtx, sqlc.BindWaveReadinessHeadsParams{
			ReadyWindowID: uuid.NullUUID{UUID: in.ID, Valid: true},
			UpdatedAt:     tstz(in.OpenedAt),
			WaveID:        waveID,
		})
		if err != nil {
			return err
		}
		memberCount, err := querier.CountWaveMembers(txCtx, waveID)
		if err != nil {
			return err
		}
		if int64(len(heads)) != memberCount {
			return errWaveCAS
		}
		if _, err = querier.OpenWaveReadyWindowCAS(txCtx, sqlc.OpenWaveReadyWindowCASParams{
			OpenedAt:         tstz(in.OpenedAt),
			ID:               waveID,
			ExpectedRevision: expectedRevision,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errWaveCAS
			}
			return err
		}
		return nil
	})
	if err != nil {
		return r.mapWaveCAS("OpenReadyWindow", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, true, err
}

func (r *WavePostgres) MarkReady(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	participantID uuid.UUID,
	expectedWaveRevision int64,
	expectedReadinessRevision int64,
	readyAt time.Time,
) (*WaveRecord, bool, error) {
	if !validMarkReadyCommand(
		tournamentID,
		waveID,
		windowID,
		participantID,
		expectedWaveRevision,
		expectedReadinessRevision,
		readyAt,
	) {
		return nil, false, domain.ErrValidation
	}
	duplicate := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		current, err := querier.GetWave(txCtx, sqlc.GetWaveParams{ID: waveID, TournamentID: tournamentID})
		if err != nil {
			return err
		}
		if current.Revision != expectedWaveRevision {
			return errWaveCAS
		}
		_, err = querier.MarkWaveMemberReadyCAS(txCtx, sqlc.MarkWaveMemberReadyCASParams{
			ReadyAt:          tstz(readyAt),
			WaveID:           waveID,
			ParticipantID:    participantID,
			ReadyWindowID:    uuid.NullUUID{UUID: windowID, Valid: true},
			ExpectedRevision: expectedReadinessRevision,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			duplicate, err = resolveMarkReadyConflict(txCtx, querier, waveID, windowID, participantID)
			return err
		}
		if err != nil {
			return err
		}
		readyCount, err := querier.CountWaveReadiness(
			txCtx,
			uuid.NullUUID{UUID: windowID, Valid: true},
		)
		if err != nil {
			return err
		}
		memberCount, err := querier.CountWaveMembers(txCtx, waveID)
		if err != nil {
			return err
		}
		if memberCount < 2 || readyCount > memberCount {
			return domain.ErrValidation
		}
		_, err = querier.MarkWaveReadinessCAS(txCtx, sqlc.MarkWaveReadinessCASParams{
			AllReady:         readyCount == memberCount,
			UpdatedAt:        tstz(readyAt),
			ID:               waveID,
			ExpectedRevision: expectedWaveRevision,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errWaveCAS
		}
		return err
	})
	if err != nil {
		return r.mapWaveCAS("MarkReady", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, !duplicate, err
}

func (r *WavePostgres) Start(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	expectedRevision int64,
	startedAt time.Time,
) (*WaveRecord, bool, error) {
	if tournamentID == uuid.Nil || waveID == uuid.Nil || windowID == uuid.Nil || expectedRevision < 1 ||
		!validServerTime(startedAt) {
		return nil, false, domain.ErrValidation
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, err := querier.ConsumeReadyWindowCAS(txCtx, sqlc.ConsumeReadyWindowCASParams{
			StartedAt: tstz(startedAt),
			ID:        windowID,
			WaveID:    waveID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errWaveCAS
			}
			return err
		}
		if _, err := querier.StartWaveCAS(txCtx, sqlc.StartWaveCASParams{
			StartedAt:        tstz(startedAt),
			ID:               waveID,
			TournamentID:     tournamentID,
			ExpectedRevision: expectedRevision,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errWaveCAS
			}
			return err
		}
		return nil
	})
	if err != nil {
		return r.mapWaveCAS("Start", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, true, err
}

func (r *WavePostgres) ExpireReadyWindow(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	expectedRevision int64,
	expiredAt time.Time,
) (*WaveRecord, bool, error) {
	if tournamentID == uuid.Nil || waveID == uuid.Nil || windowID == uuid.Nil || expectedRevision < 1 ||
		!validServerTime(expiredAt) {
		return nil, false, domain.ErrValidation
	}
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		window, err := querier.GetReadyWindow(txCtx, waveID)
		if err != nil {
			return err
		}
		if window.ID != windowID || !expiredAt.After(window.Deadline.Time) {
			return domain.ErrValidation
		}
		if _, err = querier.ClearWaveReadinessHeads(txCtx, sqlc.ClearWaveReadinessHeadsParams{
			UpdatedAt:     tstz(expiredAt),
			WaveID:        waveID,
			ReadyWindowID: uuid.NullUUID{UUID: windowID, Valid: true},
		}); err != nil {
			return err
		}
		if _, err = querier.CloseReadyWindowCAS(txCtx, sqlc.CloseReadyWindowCASParams{
			NextState:     string(domain.ReadyWindowStateExpired),
			ID:            windowID,
			WaveID:        waveID,
			ExpectedState: string(domain.ReadyWindowStateOpen),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errWaveCAS
			}
			return err
		}
		if _, err = querier.TransitionWaveCAS(txCtx, sqlc.TransitionWaveCASParams{
			NextState:        string(domain.WaveStateReadyWindowExpired),
			UpdatedAt:        tstz(expiredAt),
			ID:               waveID,
			TournamentID:     tournamentID,
			ExpectedRevision: expectedRevision,
			ExpectedState:    string(domain.WaveStateReadyWindowOpen),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errWaveCAS
			}
			return err
		}
		return nil
	})
	if err != nil {
		return r.mapWaveCAS("ExpireReadyWindow", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, true, err
}

func (r *WavePostgres) Close(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedRevision int64,
	closedAt time.Time,
) (*WaveRecord, bool, error) {
	if tournamentID == uuid.Nil || waveID == uuid.Nil || expectedRevision < 1 || !validServerTime(closedAt) {
		return nil, false, domain.ErrValidation
	}
	_, err := r.tx.Querier(ctx).CloseWaveCAS(ctx, sqlc.CloseWaveCASParams{
		RevisionID:       uuid.New(),
		ClosedAt:         tstz(closedAt),
		ID:               waveID,
		TournamentID:     tournamentID,
		ExpectedRevision: expectedRevision,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, mapRepositoryWriteError("WavePostgres - Close", err)
	}
	record, err := r.Get(ctx, tournamentID, waveID)
	return record, true, err
}

func validateWaveCreateInput(in WaveCreateInput) error {
	if !validWaveCreateMetadata(in) {
		return domain.ErrValidation
	}
	participants, err := waveParticipantSet(in.ParticipantIDs)
	if err != nil {
		return err
	}
	if err := validateWaveSeries(in.Series, participants); err != nil {
		return err
	}
	if !validWaveReplacement(in.ID, in.ReplacesWaveID) {
		return domain.ErrValidation
	}
	return nil
}

var errWaveCAS = errors.New("wave compare-and-set failed")

func validOpenReadyWindowCommand(
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	expectedRevision int64,
	in ReadyWindowInput,
) bool {
	return tournamentID != uuid.Nil && waveID != uuid.Nil && expectedRevision >= 1 && in.ID != uuid.Nil &&
		!in.RevisionID.IsZero() && validServerTime(in.OpenedAt) && validServerTime(in.Deadline) &&
		in.Deadline.After(in.OpenedAt)
}

func validMarkReadyCommand(
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	participantID uuid.UUID,
	expectedWaveRevision int64,
	expectedReadinessRevision int64,
	readyAt time.Time,
) bool {
	return tournamentID != uuid.Nil && waveID != uuid.Nil && windowID != uuid.Nil &&
		participantID != uuid.Nil && expectedWaveRevision >= 1 && expectedReadinessRevision >= 1 &&
		validServerTime(readyAt)
}
