package testbots

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

//nolint:gocyclo // Dispatch only the server-required player action and validate its required context.
func (e *Engine) step(ctx context.Context, c *client, b *Bot, m Metadata) error {
	root := "/api/v1/tournaments/" + e.run.TournamentID + "/participant"
	if string(m.Tournament.State) == "registration" {
		var admission api.TournamentAdmissionView
		if err := c.request(ctx, "GET", root+"/queue", nil, "", &admission); err != nil {
			return err
		}
		if string(admission.Status) == "not_registered" {
			b.Message = "Вступление"
			return e.command(ctx, c, b, root+"/queue", nil)
		}
		if string(admission.Status) == "registered" || string(admission.Status) == "invited" {
			b.Message = "Подтверждение участия"
			return e.command(ctx, c, b, root+"/queue/check-in", nil)
		}
		b.Message = "Ожидает фиксации состава"
		return nil
	}
	if !c.isConnected() {
		b.Message = "Подключение к турниру"
		// Revalidate the session even while the socket is reconnecting.
		var me api.CurrentPlayerResponse
		e.nextTry[b.PlayerID] = time.Now().Add(5 * time.Second)
		return c.request(ctx, "GET", "/api/v1/players/me", nil, "", &me)
	}
	if string(m.Tournament.State) == "technical_pause" {
		b.Message = "Техническая пауза турнира"
		return nil
	}
	if string(m.Tournament.State) == "golden" {
		return e.goldenStep(ctx, c, b, root)
	}
	var s api.ParticipantRecoverySnapshot
	if err := c.request(ctx, "GET", root+"/snapshot", nil, "", &s); err != nil {
		return err
	}
	revision := s.NextCursor.ProjectionRevision
	if s.Series != nil && (s.Series.FirstParticipantId.String() == e.run.HumanParticipantID || s.Series.SecondParticipantId.String() == e.run.HumanParticipantID) {
		e.policy(*s.Series)
	}
	switch string(s.Lobby.RequiredAction) {
	case "ready":
		if s.Wave == nil {
			return errors.New("missing ready window")
		}
		b.Message = "Готовность"
		return e.command(ctx, c, b, root+"/waves/"+s.Wave.Id.String()+"/ready", api.ParticipantReadyRequest{ExpectedProjectionRevision: revision, Ready: true})
	case "draft":
		d := s.Draft
		if d == nil || d.CurrentActorId == nil || d.CurrentAction == nil || d.CurrentActorId.String() != b.ParticipantID || len(d.LegalCategories) == 0 {
			b.Message = "Ожидает ход драфта"
			return nil
		}
		b.Message = "Ход драфта"
		return e.command(ctx, c, b, root+"/series/"+d.SeriesId.String()+"/draft/actions", api.ParticipantDraftActionRequest{ExpectedProjectionRevision: revision, ExpectedDraftRevision: d.Revision, ExpectedTurn: d.Turn, Action: *d.CurrentAction, Category: categories(d.LegalCategories)[0]})
	case "review_result":
		if s.Series == nil {
			return nil
		}
		if e.run.Acknowledged == nil {
			e.run.Acknowledged = map[string]bool{}
		}
		receipt := b.PlayerID + ":" + s.Series.Id.String()
		if e.run.Acknowledged[receipt] {
			b.Message = "Ожидает следующую стадию"
			return nil
		}
		b.Message = "Подтверждение результата"
		if err := e.command(ctx, c, b, root+"/series/"+s.Series.Id.String()+"/post-series", api.ParticipantPostSeriesRequest{ExpectedProjectionRevision: revision, Action: api.ParticipantPostSeriesRequestActionAcknowledgeResult}); err != nil {
			return err
		}
		e.run.Acknowledged[receipt] = true
		return e.save()
	case "play":
		return e.play(ctx, c, b, root, s)
	default:
		b.Message = "Ожидает следующую стадию"
		return nil
	}
}
func (e *Engine) play(ctx context.Context, c *client, b *Bot, root string, s api.ParticipantRecoverySnapshot) error {
	a := s.Assignment
	if a == nil || s.Series == nil || a.Context.StartedAt == nil || string(a.Context.GameState) != "active" {
		b.Message = "Ожидает начало игры"
		return nil
	}
	answer, err := e.answer(a.ActiveSnapshot.TaskId, a.ActiveSnapshot.Version)
	if err != nil {
		return err
	}
	own := b.ParticipantID
	opponent := s.Series.FirstParticipantId.String()
	if opponent == own {
		opponent = s.Series.SecondParticipantId.String()
	}
	shouldSolve := e.slot(own) < e.slot(opponent)
	delay := 5
	if opponent == e.run.HumanParticipantID {
		policy := e.policy(*s.Series)
		shouldSolve = policy.Outcome == "bot_wins"
		delay = policy.Delay
	}
	if e.run.Scenario == "golden" && string(a.Context.Stage) == "swiss" {
		shouldSolve = e.slot(own) < e.slot(opponent)
	}
	if !shouldSolve {
		b.Message = "Ожидает ответ соперника"
		return nil
	}
	now := time.Now()
	if a.Context.EffectiveDeadline == nil || !now.Before(*a.Context.EffectiveDeadline) {
		b.Message = "Время игры истекло"
		return nil
	}
	if now.Before(a.Context.StartedAt.Add(time.Duration(delay) * time.Second)) {
		b.Message = "Ожидает отправку ответа"
		return nil
	}
	b.Message = "Отправка ответа"
	return e.command(ctx, c, b, root+"/series/"+s.Series.Id.String()+"/games/"+a.Context.GameId.String()+"/submissions", api.ParticipantSubmissionRequest{ExpectedProjectionRevision: s.NextCursor.ProjectionRevision, SubmittedFlag: &answer})
}

//nolint:gocyclo // Golden readiness, task version and deadline must agree before submission.
func (e *Engine) goldenStep(ctx context.Context, c *client, b *Bot, root string) error {
	var g api.GoldenParticipantResponse
	err := c.request(ctx, "GET", root+"/golden", nil, "", &g)
	if err != nil {
		var status *apiError
		if errors.As(err, &status) && (status.Status == 404 || status.Status == 409) {
			b.Message = "Ожидает открытие Golden"
			return nil
		}
		return err
	}
	if (string(g.State) == "prepared" || string(g.State) == "ready") && !g.Ready {
		b.Message = "Готовность к Golden"
		return e.command(ctx, c, b, root+"/golden/ready", api.GoldenReadyRequest{AttemptId: g.AttemptId, ReadyWindowId: g.ReadyWindowId, ExpectedRuntimeRevision: g.RuntimeRevision, Ready: true})
	}
	if string(g.State) != "active" || g.Submitted || g.Task == nil || g.StartedAt == nil || g.Deadline == nil {
		b.Message = "Ожидает организатора Golden"
		return nil
	}
	answer, err := e.answer(g.Task.TaskId, g.Task.Version)
	if err != nil {
		return err
	}
	// Leave a clear window for the human to solve before bots finish the group.
	delay := time.Duration(60+b.Slot*2) * time.Second
	if time.Now().Before(g.StartedAt.Add(delay)) {
		b.Message = "Golden: время для ответа игрока"
		return nil
	}
	if !time.Now().Before(*g.Deadline) {
		b.Message = "Golden: время истекло"
		return nil
	}
	b.Message = "Ответ Golden"
	return e.command(ctx, c, b, root+"/golden/submissions", api.GoldenSubmissionRequest{AttemptId: g.AttemptId, ReadyWindowId: g.ReadyWindowId, ExpectedRuntimeRevision: g.RuntimeRevision, SubmittedFlag: answer})
}

//nolint:gocyclo // Every answer lookup revalidates the human session and current normal or Golden assignment.
func (e *Engine) humanAnswer(ctx context.Context, id, cookie string) (string, error) {
	if e.run == nil || e.run.TournamentID != id {
		return "", userError("Прогон не запущен.")
	}
	c := newClient(e.cfg)
	c.cookie = cookie
	var me api.CurrentPlayerResponse
	if err := c.request(ctx, "GET", "/api/v1/players/me", nil, "", &me); err != nil || me.Player.Id.String() != e.cfg.ControllerID {
		return "", userError("Недействительная сессия тестировщика.")
	}
	root := "/api/v1/tournaments/" + id + "/participant"
	var s api.ParticipantRecoverySnapshot
	if err := c.request(ctx, "GET", root+"/snapshot", nil, "", &s); err != nil {
		return "", userError("Текущее назначение недоступно.")
	}
	if string(s.Lobby.State) == "golden" {
		var g api.GoldenParticipantResponse
		if err := c.request(ctx, "GET", root+"/golden", nil, "", &g); err != nil || g.Task == nil || string(g.State) != "active" || g.Submitted || g.Deadline == nil || !time.Now().Before(*g.Deadline) {
			return "", userError("Нет активной задачи Golden.")
		}
		return e.answer(g.Task.TaskId, g.Task.Version)
	}
	if s.Assignment == nil || string(s.Assignment.Context.GameState) != "active" || s.Assignment.Context.EffectiveDeadline == nil || !time.Now().Before(*s.Assignment.Context.EffectiveDeadline) {
		return "", userError("Нет активной задачи.")
	}
	return e.answer(s.Assignment.ActiveSnapshot.TaskId, s.Assignment.ActiveSnapshot.Version)
}
func (e *Engine) resume(ctx context.Context) error {
	if e.run == nil {
		return userError("Прогон не запущен.")
	}
	m, err := e.metadata(ctx, e.run.TournamentID)
	if err != nil {
		return userError("Не удалось проверить текущее состояние.")
	}
	if e.run.Scenario == "golden" {
		if err = validateGolden(e.run, m); err != nil {
			return err
		}
	}
	for i := range e.run.Bots {
		e.run.Bots[i].Status = "waiting"
		e.run.Bots[i].Message = "Сверка состояния"
	}
	e.serverFailures = nil
	e.run.Status = "running"
	e.run.Message = fmt.Sprintf("Подключено ботов: %d", len(e.run.Bots))
	if err := e.save(); err != nil {
		e.run.Status = "paused"
		e.run.Message = "Не удалось сохранить прогон. Проверьте хранилище состояния."
		return err
	}
	return nil
}
