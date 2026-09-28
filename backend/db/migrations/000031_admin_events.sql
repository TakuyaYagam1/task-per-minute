-- +goose Up
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

-- Admin dashboards only need a topic invalidation. The subscriber validates
-- the topic and never forwards table names or notification payloads.
CREATE FUNCTION public.notify_admin_changes() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF to_jsonb(NEW) IS NOT DISTINCT FROM to_jsonb(OLD) THEN
            RETURN NULL;
        END IF;
    END IF;
    PERFORM pg_notify('admin_changes', TG_ARGV[0]);
    RETURN NULL;
END;
$$;

CREATE FUNCTION public.notify_admin_players_changed_if_changed() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF to_jsonb(NEW) IS NOT DISTINCT FROM to_jsonb(OLD) THEN
            RETURN NULL;
        END IF;
    END IF;
    PERFORM pg_notify(
        'admin_players_changed',
        json_build_object('table', TG_TABLE_NAME, 'op', TG_OP)::text
    );
    RETURN NULL;
END;
$$;

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tasks
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tasks');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.task_versions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tasks');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.task_version_health_attestations
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tasks');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.task_pool_publications
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tasks');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.task_pool_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tasks');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.task_pool_version_memberships
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tasks');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournaments
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.participants
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.rosters
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.participant_reservations
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_cancellations
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_lifecycle_commands
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_roster_operations
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_progressions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_tie_groups
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_tie_group_members
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_content_configurations
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_category_pool_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_category_pool_memberships
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_content_stage_defaults
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_content_configuration_heads
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_configuration_edit_commands
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_configuration_edit_unlock_intents
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_configuration_edit_artifacts
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_configuration_edit_invalidations
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.swiss_byes
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.swiss_opponent_history
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.swiss_pairing_commands
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.swiss_pairing_members
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.swiss_pairings
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.swiss_repeat_overrides
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.swiss_rounds
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.swiss_wave_links
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.waves
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.wave_members
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.wave_series
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.wave_readiness
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.wave_control_commands
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.wave_member_routes
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.series
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.game_attempts
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.game_slots
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.ready_windows
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.readiness_events
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.pauses
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.drafts
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.draft_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.draft_actions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_playoff_evidence
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_playoff_final_advancements
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_playoff_final_initializations
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_playoff_final_progressions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_playoff_finals
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_playoff_golden_settlements
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.tournament_stage_playoff_semifinals
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_attempt_submission_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_attempts
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_group_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_memberships
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_position_commits
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_position_ledger_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_provisional_submissions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_ready_disconnects
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_recovery_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_runtime_assignments
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_runtime_commands
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_runtime_heads
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_allocations
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_attempt_members
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_attempts
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_members
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_no_show_participants
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_no_show_resolutions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_ready_events
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_ready_window_participants
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_ready_windows
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_state_transitions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.golden_terminal_position_evidence
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.audit_events
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.official_result_heads
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.official_result_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.result_commits
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.result_events
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.result_projection_evidence
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.series_score_heads
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.series_score_revision_adjudications
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.series_score_revision_attempts
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.series_score_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.submission_events
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.normal_no_show_commit_games
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.normal_no_show_commits
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.operator_forfeit_commits
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.operator_result_commands
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.swiss_point_ledger_entries
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.result_correction_commits
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.projection_artifacts
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.projection_artifact_members
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.projection_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.result_projection_dependencies
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.result_projection_nodes
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.correction_projection_decisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

CREATE TRIGGER admin_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.correction_projection_bindings
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_changes('tournaments');

-- Player leaderboard stats are derived from these result tables. Reuse the
-- existing player invalidation channel so both player SSE consumers refresh.
CREATE TRIGGER admin_players_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.participants
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_players_changed_if_changed();

CREATE TRIGGER admin_players_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.game_attempts
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_players_changed_if_changed();

CREATE TRIGGER admin_players_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.official_result_heads
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_players_changed_if_changed();

CREATE TRIGGER admin_players_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.official_result_revisions
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_players_changed_if_changed();

CREATE TRIGGER admin_players_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.result_events
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_players_changed_if_changed();

CREATE TRIGGER admin_players_changes_notify AFTER INSERT OR UPDATE OR DELETE ON public.submission_events
    FOR EACH ROW EXECUTE FUNCTION public.notify_admin_players_changed_if_changed();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SET LOCAL lock_timeout = '5s';

DROP TRIGGER IF EXISTS admin_players_changes_notify ON public.participants;
DROP TRIGGER IF EXISTS admin_players_changes_notify ON public.game_attempts;
DROP TRIGGER IF EXISTS admin_players_changes_notify ON public.official_result_heads;
DROP TRIGGER IF EXISTS admin_players_changes_notify ON public.official_result_revisions;
DROP TRIGGER IF EXISTS admin_players_changes_notify ON public.result_events;
DROP TRIGGER IF EXISTS admin_players_changes_notify ON public.submission_events;

DROP TRIGGER IF EXISTS admin_changes_notify ON public.audit_events;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.official_result_heads;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.official_result_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.result_commits;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.result_events;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.result_projection_evidence;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.series_score_heads;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.series_score_revision_adjudications;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.series_score_revision_attempts;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.series_score_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.submission_events;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.normal_no_show_commit_games;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.normal_no_show_commits;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.operator_forfeit_commits;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.operator_result_commands;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.swiss_point_ledger_entries;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.result_correction_commits;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.projection_artifacts;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.projection_artifact_members;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.projection_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.result_projection_dependencies;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.result_projection_nodes;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.correction_projection_decisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.correction_projection_bindings;

DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournaments;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.participants;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.rosters;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.participant_reservations;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_cancellations;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_lifecycle_commands;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_roster_operations;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_progressions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_tie_groups;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_tie_group_members;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_content_configurations;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_category_pool_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_category_pool_memberships;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_content_stage_defaults;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_content_configuration_heads;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_configuration_edit_commands;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_configuration_edit_unlock_intents;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_configuration_edit_artifacts;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_configuration_edit_invalidations;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.swiss_byes;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.swiss_opponent_history;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.swiss_pairing_commands;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.swiss_pairing_members;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.swiss_pairings;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.swiss_repeat_overrides;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.swiss_rounds;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.swiss_wave_links;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.waves;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.wave_members;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.wave_series;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.wave_readiness;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.wave_control_commands;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.wave_member_routes;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.series;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.game_attempts;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.game_slots;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.ready_windows;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.readiness_events;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.pauses;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.drafts;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.draft_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.draft_actions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_playoff_evidence;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_playoff_final_advancements;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_playoff_final_initializations;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_playoff_final_progressions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_playoff_finals;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_playoff_golden_settlements;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.tournament_stage_playoff_semifinals;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_attempt_submission_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_attempts;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_group_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_memberships;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_position_commits;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_position_ledger_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_provisional_submissions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_ready_disconnects;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_recovery_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_runtime_assignments;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_runtime_commands;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_runtime_heads;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_allocations;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_attempt_members;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_attempts;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_members;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_no_show_participants;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_no_show_resolutions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_ready_events;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_ready_window_participants;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_ready_windows;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_state_transitions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.golden_terminal_position_evidence;

DROP TRIGGER IF EXISTS admin_changes_notify ON public.tasks;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.task_versions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.task_version_health_attestations;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.task_pool_publications;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.task_pool_revisions;
DROP TRIGGER IF EXISTS admin_changes_notify ON public.task_pool_version_memberships;

DROP FUNCTION IF EXISTS public.notify_admin_players_changed_if_changed();
DROP FUNCTION IF EXISTS public.notify_admin_changes();

-- +goose StatementEnd
