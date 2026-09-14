package tournament

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidOperatorSnapshot = errors.New("invalid tournament operator snapshot")

type OperatorSnapshotAccess struct {
	Authenticated bool
	TournamentID  uuid.UUID
	OperatorID    uuid.UUID
}

type OperatorSnapshotInput struct {
	TournamentID uuid.UUID
	Revision     int64
	LastSequence int64
	Waves        []OperatorWaveInput
	Presence     []OperatorPresenceInput
	Replays      []OperatorReplayInput
	Pause        *OperatorPauseInput
	AuditLinks   []OperatorAuditLinkInput
	Golden       []OperatorGoldenGroupInput
}

type OperatorWaveInput struct {
	TournamentID   uuid.UUID
	WaveID         uuid.UUID
	State          string
	WindowDeadline *time.Time
	Members        []OperatorWaveMemberInput
}

type OperatorWaveMemberInput struct {
	ParticipantID     uuid.UUID
	SeriesID          *uuid.UUID
	Ready             bool
	ReadinessRevision int64
}

type OperatorPresenceInput struct {
	TournamentID  uuid.UUID
	ParticipantID uuid.UUID
	SeriesID      uuid.UUID
	State         string
	PresenceEpoch int64
	UpdatedAt     time.Time
}

type OperatorReplayInput struct {
	TournamentID      uuid.UUID
	SeriesID          uuid.UUID
	SlotID            uuid.UUID
	FailedGameID      uuid.UUID
	ReplacementGameID uuid.UUID
	ReplacementWaveID uuid.UUID
	State             string
	Revision          int64
}

type OperatorPauseInput struct {
	TournamentID      uuid.UUID
	PauseID           uuid.UUID
	State             string
	Reason            string
	PausedAt          time.Time
	GraphRevision     int64
	GameID            *uuid.UUID
	FrozenRemainingMS *int64
	ReconnectDeadline *time.Time
}

type OperatorAuditLinkInput struct {
	TournamentID             uuid.UUID
	AuditEventID             uuid.UUID
	EntityKind               string
	EntityID                 uuid.UUID
	OfficialResultRevisionID uuid.UUID
}

type OperatorGoldenGroupInput struct {
	GroupID         uuid.UUID
	GroupRevisionID uuid.UUID
	AttemptID       uuid.UUID
	// RuntimeRevision is the authoritative Golden projection fence. The generic
	// standings projection revision may remain unchanged while this advances.
	RuntimeRevision int64
	ReadyWindowID   uuid.UUID
	State           string
	PositionFrom    int
	PositionTo      int
	StartedAt       *time.Time
	Deadline        *time.Time
	Members         []OperatorGoldenMemberInput
}

type OperatorGoldenMemberInput struct {
	ParticipantID uuid.UUID
	Ready         bool
	Submitted     bool
	Position      *int
}

type OperatorSnapshot struct {
	TournamentID uuid.UUID             `json:"tournament_id"`
	Revision     int64                 `json:"revision"`
	LastSequence int64                 `json:"last_sequence"`
	Waves        []OperatorWave        `json:"waves"`
	Presence     []OperatorPresence    `json:"presence"`
	Replays      []OperatorReplay      `json:"replays"`
	Pause        *OperatorPause        `json:"pause,omitempty"`
	AuditLinks   []OperatorAuditLink   `json:"audit_links"`
	Golden       []OperatorGoldenGroup `json:"golden"`
}

type OperatorWave struct {
	WaveID         uuid.UUID            `json:"wave_id"`
	State          string               `json:"state"`
	WindowDeadline *time.Time           `json:"window_deadline,omitempty"`
	Members        []OperatorWaveMember `json:"members"`
}

type OperatorWaveMember struct {
	ParticipantID     uuid.UUID  `json:"participant_id"`
	SeriesID          *uuid.UUID `json:"series_id,omitempty"`
	Ready             bool       `json:"ready"`
	ReadinessRevision int64      `json:"readiness_revision"`
}

type OperatorPresence struct {
	ParticipantID uuid.UUID `json:"participant_id"`
	SeriesID      uuid.UUID `json:"series_id"`
	State         string    `json:"state"`
	PresenceEpoch int64     `json:"presence_epoch"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type OperatorReplay struct {
	SeriesID          uuid.UUID `json:"series_id"`
	SlotID            uuid.UUID `json:"slot_id"`
	FailedGameID      uuid.UUID `json:"failed_game_id"`
	ReplacementGameID uuid.UUID `json:"replacement_game_id"`
	ReplacementWaveID uuid.UUID `json:"replacement_wave_id"`
	State             string    `json:"state"`
	Revision          int64     `json:"revision"`
}

type OperatorPause struct {
	PauseID           uuid.UUID  `json:"pause_id"`
	State             string     `json:"state"`
	Reason            string     `json:"reason"`
	PausedAt          time.Time  `json:"paused_at"`
	GraphRevision     int64      `json:"graph_revision"`
	GameID            *uuid.UUID `json:"game_id,omitempty"`
	FrozenRemainingMS *int64     `json:"frozen_remaining_ms,omitempty"`
	ReconnectDeadline *time.Time `json:"reconnect_deadline,omitempty"`
}

type OperatorAuditLink struct {
	AuditEventID             uuid.UUID `json:"audit_event_id"`
	EntityKind               string    `json:"entity_kind"`
	EntityID                 uuid.UUID `json:"entity_id"`
	OfficialResultRevisionID uuid.UUID `json:"official_result_revision_id"`
}

type OperatorGoldenGroup struct {
	GroupID         uuid.UUID `json:"group_id"`
	GroupRevisionID uuid.UUID `json:"group_revision_id"`
	AttemptID       uuid.UUID `json:"attempt_id"`
	// RuntimeRevision is the wire-visible Golden projection fence.
	RuntimeRevision int64                  `json:"runtime_revision"`
	ReadyWindowID   uuid.UUID              `json:"ready_window_id"`
	State           string                 `json:"state"`
	PositionFrom    int                    `json:"position_from"`
	PositionTo      int                    `json:"position_to"`
	StartedAt       *time.Time             `json:"started_at,omitempty"`
	Deadline        *time.Time             `json:"deadline,omitempty"`
	Members         []OperatorGoldenMember `json:"members"`
}

type OperatorGoldenMember struct {
	ParticipantID uuid.UUID `json:"participant_id"`
	Ready         bool      `json:"ready"`
	Submitted     bool      `json:"submitted"`
	Position      *int      `json:"position,omitempty"`
}

//nolint:gocyclo // The constructor copies each operator view while enforcing authenticated tournament scope.
func NewOperatorSnapshot(access OperatorSnapshotAccess, input OperatorSnapshotInput) (OperatorSnapshot, error) {
	if !access.Authenticated || access.TournamentID == uuid.Nil || access.OperatorID == uuid.Nil || input.TournamentID != access.TournamentID {
		return OperatorSnapshot{}, fmt.Errorf("%w: access denied", ErrInvalidOperatorSnapshot)
	}
	if input.Revision < 1 || input.LastSequence < 0 {
		return OperatorSnapshot{}, fmt.Errorf("%w: invalid cursor", ErrInvalidOperatorSnapshot)
	}
	snapshot := OperatorSnapshot{
		TournamentID: input.TournamentID,
		Revision:     input.Revision,
		LastSequence: input.LastSequence,
		Waves:        make([]OperatorWave, len(input.Waves)),
		Presence:     make([]OperatorPresence, len(input.Presence)),
		Replays:      make([]OperatorReplay, len(input.Replays)),
		AuditLinks:   make([]OperatorAuditLink, len(input.AuditLinks)),
		Golden:       make([]OperatorGoldenGroup, len(input.Golden)),
	}
	for index, wave := range input.Waves {
		if wave.TournamentID != access.TournamentID {
			return OperatorSnapshot{}, fmt.Errorf("%w: wave crosses tournament", ErrInvalidOperatorSnapshot)
		}
		view := OperatorWave{WaveID: wave.WaveID, State: wave.State, WindowDeadline: cloneTime(wave.WindowDeadline), Members: make([]OperatorWaveMember, len(wave.Members))}
		for memberIndex, member := range wave.Members {
			view.Members[memberIndex] = OperatorWaveMember{
				ParticipantID:     member.ParticipantID,
				SeriesID:          cloneUUID(member.SeriesID),
				Ready:             member.Ready,
				ReadinessRevision: member.ReadinessRevision,
			}
		}
		snapshot.Waves[index] = view
	}
	for index, presence := range input.Presence {
		if presence.TournamentID != access.TournamentID {
			return OperatorSnapshot{}, fmt.Errorf("%w: presence crosses tournament", ErrInvalidOperatorSnapshot)
		}
		snapshot.Presence[index] = OperatorPresence{ParticipantID: presence.ParticipantID, SeriesID: presence.SeriesID, State: presence.State, PresenceEpoch: presence.PresenceEpoch, UpdatedAt: presence.UpdatedAt}
	}
	for index, replay := range input.Replays {
		if replay.TournamentID != access.TournamentID {
			return OperatorSnapshot{}, fmt.Errorf("%w: replay crosses tournament", ErrInvalidOperatorSnapshot)
		}
		snapshot.Replays[index] = OperatorReplay{SeriesID: replay.SeriesID, SlotID: replay.SlotID, FailedGameID: replay.FailedGameID, ReplacementGameID: replay.ReplacementGameID, ReplacementWaveID: replay.ReplacementWaveID, State: replay.State, Revision: replay.Revision}
	}
	if input.Pause != nil {
		if input.Pause.TournamentID != access.TournamentID {
			return OperatorSnapshot{}, fmt.Errorf("%w: pause crosses tournament", ErrInvalidOperatorSnapshot)
		}
		snapshot.Pause = &OperatorPause{
			PauseID:           input.Pause.PauseID,
			State:             input.Pause.State,
			Reason:            input.Pause.Reason,
			PausedAt:          input.Pause.PausedAt,
			GraphRevision:     input.Pause.GraphRevision,
			GameID:            cloneUUID(input.Pause.GameID),
			FrozenRemainingMS: cloneInt64(input.Pause.FrozenRemainingMS),
			ReconnectDeadline: cloneTime(input.Pause.ReconnectDeadline),
		}
	}
	for index, link := range input.AuditLinks {
		if link.TournamentID != access.TournamentID {
			return OperatorSnapshot{}, fmt.Errorf("%w: audit link crosses tournament", ErrInvalidOperatorSnapshot)
		}
		snapshot.AuditLinks[index] = OperatorAuditLink{AuditEventID: link.AuditEventID, EntityKind: link.EntityKind, EntityID: link.EntityID, OfficialResultRevisionID: link.OfficialResultRevisionID}
	}
	for index, group := range input.Golden {
		if group.GroupID == uuid.Nil || group.GroupRevisionID == uuid.Nil || group.AttemptID == uuid.Nil ||
			group.RuntimeRevision < 1 || group.ReadyWindowID == uuid.Nil ||
			!validRealtimeString(group.State) || group.PositionFrom < 1 || group.PositionTo < group.PositionFrom ||
			group.PositionTo > 16 || group.Members == nil || len(group.Members) < 2 ||
			!validOptionalUTC(group.StartedAt) || !validOptionalUTC(group.Deadline) {
			return OperatorSnapshot{}, fmt.Errorf("%w: invalid Golden group", ErrInvalidOperatorSnapshot)
		}
		view := OperatorGoldenGroup{
			GroupID: group.GroupID, GroupRevisionID: group.GroupRevisionID, AttemptID: group.AttemptID,
			RuntimeRevision: group.RuntimeRevision, ReadyWindowID: group.ReadyWindowID,
			State: group.State, PositionFrom: group.PositionFrom, PositionTo: group.PositionTo,
			StartedAt: cloneTime(group.StartedAt), Deadline: cloneTime(group.Deadline),
			Members: make([]OperatorGoldenMember, len(group.Members)),
		}
		for memberIndex, member := range group.Members {
			if member.ParticipantID == uuid.Nil || (member.Position != nil && (*member.Position < 1 || *member.Position > 16)) {
				return OperatorSnapshot{}, fmt.Errorf("%w: invalid Golden member", ErrInvalidOperatorSnapshot)
			}
			view.Members[memberIndex] = OperatorGoldenMember{
				ParticipantID: member.ParticipantID, Ready: member.Ready, Submitted: member.Submitted,
				Position: cloneInt(member.Position),
			}
		}
		snapshot.Golden[index] = view
	}
	if err := snapshot.Validate(); err != nil {
		return OperatorSnapshot{}, err
	}
	return snapshot, nil
}

//nolint:gocyclo // One operator boundary validates all actionable evidence without exposing raw private data.
func (s OperatorSnapshot) Validate() error {
	if s.TournamentID == uuid.Nil || s.Revision < 1 || s.LastSequence < 0 {
		return fmt.Errorf("%w: invalid identity or cursor", ErrInvalidOperatorSnapshot)
	}
	if s.Waves == nil || s.Presence == nil || s.Replays == nil || s.AuditLinks == nil || s.Golden == nil ||
		!validRealtimeCollections(len(s.Waves), len(s.Presence), len(s.Replays), len(s.AuditLinks), len(s.Golden)) {
		return fmt.Errorf("%w: operator collections must be arrays", ErrInvalidOperatorSnapshot)
	}
	for _, wave := range s.Waves {
		if wave.WaveID == uuid.Nil || !validRealtimeString(wave.State) || !validOptionalUTC(wave.WindowDeadline) || wave.Members == nil || !validRealtimeCollections(len(wave.Members)) {
			return fmt.Errorf("%w: invalid wave readiness", ErrInvalidOperatorSnapshot)
		}
		for _, member := range wave.Members {
			if member.ParticipantID == uuid.Nil ||
				(member.SeriesID != nil && *member.SeriesID == uuid.Nil) || member.ReadinessRevision < 1 {
				return fmt.Errorf("%w: invalid wave member readiness", ErrInvalidOperatorSnapshot)
			}
		}
	}
	for _, presence := range s.Presence {
		if presence.ParticipantID == uuid.Nil || presence.SeriesID == uuid.Nil || !validRealtimeString(presence.State) || presence.PresenceEpoch < 1 || !isServerUTC(presence.UpdatedAt) {
			return fmt.Errorf("%w: invalid presence", ErrInvalidOperatorSnapshot)
		}
	}
	for _, replay := range s.Replays {
		if replay.SeriesID == uuid.Nil || replay.SlotID == uuid.Nil || replay.FailedGameID == uuid.Nil || replay.ReplacementGameID == uuid.Nil || replay.ReplacementWaveID == uuid.Nil || !validRealtimeString(replay.State) || replay.Revision < 1 {
			return fmt.Errorf("%w: invalid replay", ErrInvalidOperatorSnapshot)
		}
	}
	if s.Pause != nil {
		if s.Pause.PauseID == uuid.Nil || !validRealtimeString(s.Pause.State) || !validRealtimeString(s.Pause.Reason) || !isServerUTC(s.Pause.PausedAt) || s.Pause.GraphRevision < 1 ||
			(s.Pause.GameID == nil) != (s.Pause.FrozenRemainingMS == nil) ||
			(s.Pause.GameID != nil && *s.Pause.GameID == uuid.Nil) ||
			(s.Pause.FrozenRemainingMS != nil && *s.Pause.FrozenRemainingMS <= 0) ||
			(s.Pause.ReconnectDeadline != nil && s.Pause.GameID == nil) || !validOptionalUTC(s.Pause.ReconnectDeadline) {
			return fmt.Errorf("%w: invalid pause", ErrInvalidOperatorSnapshot)
		}
	}
	for _, link := range s.AuditLinks {
		if link.AuditEventID == uuid.Nil || link.EntityID == uuid.Nil || link.OfficialResultRevisionID == uuid.Nil || !validRealtimeString(link.EntityKind) {
			return fmt.Errorf("%w: invalid audit link", ErrInvalidOperatorSnapshot)
		}
	}
	for _, group := range s.Golden {
		if group.GroupID == uuid.Nil || group.GroupRevisionID == uuid.Nil || group.AttemptID == uuid.Nil ||
			group.RuntimeRevision < 1 || group.ReadyWindowID == uuid.Nil ||
			!validRealtimeString(group.State) || group.PositionFrom < 1 || group.PositionTo < group.PositionFrom ||
			group.PositionTo > 16 || len(group.Members) < 2 || !validOptionalUTC(group.StartedAt) ||
			!validOptionalUTC(group.Deadline) {
			return fmt.Errorf("%w: invalid Golden group", ErrInvalidOperatorSnapshot)
		}
	}
	if !valueWithinWireLimits(s) {
		return fmt.Errorf("%w: snapshot exceeds wire limits", ErrInvalidOperatorSnapshot)
	}
	return nil
}

func (s OperatorSnapshot) clone() OperatorSnapshot {
	clone := s
	clone.Waves = make([]OperatorWave, len(s.Waves))
	for index, wave := range s.Waves {
		clone.Waves[index] = wave
		clone.Waves[index].WindowDeadline = cloneTime(wave.WindowDeadline)
		clone.Waves[index].Members = make([]OperatorWaveMember, len(wave.Members))
		for memberIndex, member := range wave.Members {
			clone.Waves[index].Members[memberIndex] = member
			clone.Waves[index].Members[memberIndex].SeriesID = cloneUUID(member.SeriesID)
		}
	}
	clone.Presence = append([]OperatorPresence{}, s.Presence...)
	clone.Replays = append([]OperatorReplay{}, s.Replays...)
	clone.AuditLinks = append([]OperatorAuditLink{}, s.AuditLinks...)
	clone.Golden = make([]OperatorGoldenGroup, len(s.Golden))
	for index, group := range s.Golden {
		clone.Golden[index] = group
		clone.Golden[index].StartedAt = cloneTime(group.StartedAt)
		clone.Golden[index].Deadline = cloneTime(group.Deadline)
		clone.Golden[index].Members = make([]OperatorGoldenMember, len(group.Members))
		for memberIndex, member := range group.Members {
			clone.Golden[index].Members[memberIndex] = member
			clone.Golden[index].Members[memberIndex].Position = cloneInt(member.Position)
		}
	}
	if s.Pause != nil {
		pause := *s.Pause
		pause.GameID = cloneUUID(s.Pause.GameID)
		pause.FrozenRemainingMS = cloneInt64(s.Pause.FrozenRemainingMS)
		pause.ReconnectDeadline = cloneTime(s.Pause.ReconnectDeadline)
		clone.Pause = &pause
	}
	return clone
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
