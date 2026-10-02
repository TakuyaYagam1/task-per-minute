package testbots

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/google/uuid"
)

type Engine struct {
	mu              sync.Mutex
	cfg             Config
	key             string
	accounts        []Account
	tasks           map[string]string
	catalogCounts   map[string]int
	contentRevision int64
	run             *Run
	clients         map[string]*client
	logged          map[string]bool
	nextTry         map[string]time.Time
	serverFailures  map[string]int
	service         *client
	lock            *os.File
	ctx             context.Context
}

//nolint:gocyclo // Validate the complete private manifest and restored run before creating any player clients.
func New(cfg Config) (*Engine, error) {
	if !validID(cfg.ControllerID) || cfg.StateDir == "" {
		return nil, errors.New("configure TEST_BOTS_CONTROLLER_PLAYER_ID and state directory")
	}
	var accounts struct {
		Version  int       `json:"version"`
		Applied  bool      `json:"applied"`
		Accounts []Account `json:"accounts"`
	}
	var catalog struct {
		Version         int    `json:"version"`
		ContentRevision int64  `json:"content_revision"`
		Tasks           []Task `json:"tasks"`
	}
	if privateJSON(cfg.AccountsFile, &accounts) != nil || privateJSON(cfg.CatalogFile, &catalog) != nil || !accounts.Applied || accounts.Version != 1 || len(accounts.Accounts) != 16 || catalog.Version != 1 || catalog.ContentRevision < 1 {
		return nil, errPrivateFile
	}
	key, err := os.ReadFile(cfg.KeyFile)
	keyInfo, statErr := os.Lstat(cfg.KeyFile)
	if err != nil || statErr != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode().Perm() != 0600 || len(key) != 64 {
		return nil, errPrivateFile
	}
	engine := &Engine{cfg: cfg, key: string(key), accounts: accounts.Accounts, tasks: map[string]string{}, contentRevision: catalog.ContentRevision, clients: map[string]*client{}, logged: map[string]bool{}, nextTry: map[string]time.Time{}, service: newClient(cfg)}
	engine.service.key = engine.key
	engine.catalogCounts = map[string]int{}
	identities := map[string]bool{}
	for i, a := range engine.accounts {
		if a.Username != fmt.Sprintf("demo%02d", i+1) || !validID(a.PlayerID) || len(a.Password) < 20 || identities[a.PlayerID] {
			return nil, errPrivateFile
		}
		identities[a.PlayerID] = true
	}
	for _, task := range catalog.Tasks {
		k := taskKey(task.TaskID, task.Version)
		if !validID(task.TaskID) || task.Version < 1 || task.Answer == "" || engine.tasks[k] != "" {
			return nil, errPrivateFile
		}
		engine.tasks[k] = task.Answer
		engine.catalogCounts[task.Kind+":"+task.Category]++
	}
	if len(engine.tasks) < 54 {
		return nil, errors.New("test task catalog is incomplete")
	}
	if err = os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, errPrivateFile
	}
	engine.lock, err = os.OpenFile(filepath.Join(cfg.StateDir, "runner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errPrivateFile
	}
	if err = syscall.Flock(int(engine.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = engine.lock.Close()
		return nil, errors.New("another test runner owns the state")
	}
	state := filepath.Join(cfg.StateDir, "run.json")
	if _, err = os.Lstat(state); err == nil {
		var run Run
		if privateJSON(state, &run) != nil || !validID(run.TournamentID) || run.ControllerID != cfg.ControllerID || (run.Size != 8 && run.Size != 16) || len(run.Bots) != run.Size-1 || run.Commands == nil || run.Policies == nil {
			engine.Close()
			return nil, errPrivateFile
		}
		seenSlots, seenPlayers := map[int]bool{1: true}, map[string]bool{cfg.ControllerID: true}
		for _, bot := range run.Bots {
			known := false
			for _, account := range engine.accounts {
				if bot.PlayerID == account.PlayerID && bot.Username == account.Username {
					known = true
					break
				}
			}
			if !known || seenSlots[bot.Slot] || seenPlayers[bot.PlayerID] || bot.Slot < 0 || bot.Slot >= run.Size || (bot.ParticipantID != "" && !validID(bot.ParticipantID)) {
				engine.Close()
				return nil, errPrivateFile
			}
			seenSlots[bot.Slot], seenPlayers[bot.PlayerID] = true, true
		}
		engine.run = &run
		if run.Status != "completed" && run.Status != "cancelled" {
			run.Status = "paused"
			run.Message = "Сервис перезапущен. Проверьте турнир и нажмите Продолжить."
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		engine.Close()
		return nil, errPrivateFile
	}
	return engine, nil
}
func (e *Engine) Close() {
	for _, c := range e.clients {
		c.close()
	}
	if e.lock != nil {
		_ = e.lock.Close()
		e.lock = nil
	}
}
func (e *Engine) save() error {
	if e.run == nil {
		return nil
	}
	e.run.UpdatedAt = time.Now().UTC()
	return atomicJSON(filepath.Join(e.cfg.StateDir, "run.json"), e.run)
}
func (e *Engine) Start(ctx context.Context) {
	e.ctx = ctx
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			e.Close()
			e.mu.Unlock()
			return
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}
func (e *Engine) metadata(ctx context.Context, id string) (Metadata, error) {
	var m Metadata
	err := e.service.request(ctx, http.MethodGet, "/internal/test-bots/"+id+"/metadata", nil, "", &m)
	return m, err
}
func (e *Engine) view(id string) View {
	v := View{Available: true, TournamentID: id, Status: "idle", Message: "Вступите в турнир и заполните свободные места ботами.", Next: Policy{Outcome: "human_wins", Delay: 15}, Bots: []Bot{}}
	if r := e.run; r != nil {
		if r.TournamentID != id && r.Status != "completed" && r.Status != "cancelled" {
			v.Status = "busy"
			v.Message = "Боты заняты в другом турнире."
			return v
		}
		if r.TournamentID == id {
			v.Status = r.Status
			v.Message = r.Message
			v.Size = r.Size
			v.Scenario = r.Scenario
			v.Next = r.Next
			v.Bots = append([]Bot{}, r.Bots...)
		}
	}
	return v
}

//nolint:gocyclo // Start has one fail-closed boundary for session, roster, catalog and scenario compatibility.
func (e *Engine) startRun(ctx context.Context, id string, action Action, cookie string) error {
	if e.run != nil && e.run.Status != "completed" && e.run.Status != "cancelled" {
		return userError("Уже есть активный прогон.")
	}
	if action.Scenario != "free" && action.Scenario != "golden" {
		return userError("Выберите сценарий.")
	}
	human := newClient(e.cfg)
	human.cookie = cookie
	var me api.CurrentPlayerResponse
	if err := human.request(ctx, "GET", "/api/v1/players/me", nil, "", &me); err != nil || me.Player.Id.String() != e.cfg.ControllerID {
		return userError("Войдите в аккаунт тестировщика.")
	}
	m, err := e.metadata(ctx, id)
	if err != nil {
		return userError("Не удалось проверить турнир.")
	}
	n := int(m.Tournament.PlannedRosterSize)
	if (n != 8 && n != 16) || string(m.Tournament.State) != "registration" || m.Roster.Locked {
		return userError("Нужна открытая регистрация на 8 или 16 игроков.")
	}
	if len(m.Roster.Participants) != 1 || m.Roster.Participants[0].PlayerId.String() != e.cfg.ControllerID {
		return userError("В составе должен быть только ваш аккаунт. Вступите в турнир.")
	}
	if m.Tournament.ContentRevision != e.contentRevision || m.Configuration.ReserveCount != 0 {
		return userError("Нужен текущий тестовый каталог и нулевые резервы.")
	}
	if action.Scenario == "golden" {
		if !goldenConfig(m.Configuration) {
			return userError("Для Golden выберите Crypto без драфта в Swiss и полуфиналах, Crypto в Golden, без резервов.")
		}
		for key, minimum := range map[string]int{"normal:crypto": 35, "normal:web": 4, "normal:reverse": 3, "normal:forensics": 1, "normal:pwn": 1, "golden:crypto": 8} {
			if e.catalogCounts[key] < minimum {
				return userError("Для сценария Golden недостаточно задач проверенного тестового каталога. Повторите импорт.")
			}
		}
	}
	for _, c := range e.clients {
		c.close()
	}
	e.clients = map[string]*client{}
	e.logged = map[string]bool{}
	e.nextTry = map[string]time.Time{}
	r := &Run{TournamentID: id, ControllerID: e.cfg.ControllerID, HumanParticipantID: m.Roster.Participants[0].Id.String(), Size: n, Scenario: action.Scenario, Status: "running", Message: "Подключение ботов", Next: Policy{Outcome: "human_wins", Delay: 15}, Bots: []Bot{}, Policies: map[string]Policy{}, Commands: map[string]Command{}}
	slot := 0
	for _, a := range e.accounts {
		if a.PlayerID == e.cfg.ControllerID {
			continue
		}
		if slot == 1 {
			slot++
		}
		r.Bots = append(r.Bots, Bot{Username: a.Username, PlayerID: a.PlayerID, Slot: slot, Status: "waiting", Message: "Вход"})
		slot++
		if len(r.Bots) == n-1 {
			break
		}
	}
	e.run = r
	if err := e.save(); err != nil {
		r.Status = "paused"
		r.Message = "Не удалось сохранить прогон. Проверьте хранилище состояния."
		return err
	}
	return nil
}
func goldenConfig(c api.TournamentConfiguration) bool {
	crypto := func(stage api.TournamentConfigurationStageDefault) bool {
		return len(stage.Categories) == 1 && stage.Categories[0] == "crypto"
	}
	return c.ReserveCount == 0 && c.SwissDefault.Mode == "admin" && crypto(c.SwissDefault) && c.SemifinalDefault.Mode == "admin" && crypto(c.SemifinalDefault) && crypto(c.GoldenDefault)
}
func (e *Engine) command(ctx context.Context, c *client, b *Bot, path string, body any) error {
	raw, _ := json.Marshal(body)
	sum := sha256.Sum256(append([]byte(b.PlayerID+path), raw...))
	digest := hex.EncodeToString(sum[:])
	record, ok := e.run.Commands[digest]
	if record.Done {
		return nil
	}
	if !ok {
		record = Command{Key: newCommand()}
		e.run.Commands[digest] = record
		if err := e.save(); err != nil {
			return err
		}
	}
	err := c.request(ctx, "POST", path, body, record.Key, nil)
	if err == nil {
		record.Done = true
		e.run.Commands[digest] = record
		return e.save()
	}
	return err
}

//nolint:gocyclo // The scheduler keeps pause, terminal state, connection ownership and each player step under one lock.
func (e *Engine) tick(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.run
	if r == nil || r.Status == "completed" || r.Status == "cancelled" {
		return
	}
	m, err := e.metadata(ctx, r.TournamentID)
	if err != nil {
		r.Message = "Сервер недоступен. Ожидаем восстановления."
		return
	}
	if string(m.Tournament.State) == "completed" || string(m.Tournament.State) == "cancelled" {
		r.Status = string(m.Tournament.State)
		r.Message = "Турнир завершен."
		for _, c := range e.clients {
			c.close()
		}
		_ = e.save()
		return
	}
	allowed := map[string]bool{r.ControllerID: true}
	for _, b := range r.Bots {
		allowed[b.PlayerID] = true
	}
	for _, p := range m.Roster.Participants {
		if !allowed[p.PlayerId.String()] {
			r.Status = "paused"
			r.Message = "В составе появился участник вне тестового прогона."
			_ = e.save()
			return
		}
		for i := range r.Bots {
			if p.PlayerId.String() == r.Bots[i].PlayerID {
				r.Bots[i].ParticipantID = p.Id.String()
			}
		}
	}
	if r.Scenario == "golden" {
		if err = validateGolden(r, m); err != nil {
			r.Status = "paused"
			r.Message = err.Error()
		}
	}
	for i := range r.Bots {
		b := &r.Bots[i]
		if b.Status == "error" || time.Now().Before(e.nextTry[b.PlayerID]) {
			continue
		}
		c := e.clients[b.PlayerID]
		if c == nil {
			c = newClient(e.cfg)
			e.clients[b.PlayerID] = c
		}
		if !e.logged[b.PlayerID] {
			var account Account
			for _, a := range e.accounts {
				if a.PlayerID == b.PlayerID {
					account = a
					break
				}
			}
			err = c.login(ctx, account)
			if err == nil {
				e.logged[b.PlayerID] = true
			}
			e.handle(b, err)
			if err != nil {
				continue
			}
		}
		if b.ParticipantID != "" && m.Roster.Locked {
			c.connect(ctx, r.TournamentID)
		}
		if r.Status == "paused" {
			b.Status = "paused"
			continue
		}
		b.Status = "running"
		err = e.step(ctx, c, b, m)
		e.handle(b, err)
		// Let panel commands acquire the lock between player requests. A slow
		// participant must not block pause behind the entire roster.
		e.mu.Unlock()
		e.mu.Lock()
		if e.run != r || ctx.Err() != nil {
			return
		}
	}
	if r.Status == "running" {
		r.Message = e.guidance(m)
	}
	if err = e.save(); err != nil {
		r.Status = "paused"
		r.Message = "Не удалось сохранить состояние прогона."
	}
}

//nolint:gocyclo // Ordered tournament and wave states select one actionable operator or player instruction.
func (e *Engine) guidance(m Metadata) string {
	if m.Tournament.State == "registration" {
		return "Подтвердите участие. После подключения ботов организатор выполняет preflight, фиксирует состав и запускает Swiss."
	}
	if m.Tournament.State == "golden" {
		return "Организатор открывает Golden. Подтвердите готовность, дождитесь запуска и отправьте ответ. Боты отвечают после 60 секунд."
	}
	for _, s := range m.Series {
		if s.State == "draft" && (s.FirstParticipantId.String() == e.run.HumanParticipantID || s.SecondParticipantId.String() == e.run.HumanParticipantID) {
			return "Пройдите драфт в игровой форме. Бот выполнит свои ходы."
		}
	}
	for _, w := range m.Waves {
		if w.State == "planned" {
			return "Организатор открывает окно готовности следующей волны."
		}
		if w.State == "ready_window_open" || w.State == "ready" {
			for _, member := range w.Members {
				if member.ParticipantId.String() == e.run.HumanParticipantID && !member.Ready {
					return "Подтвердите готовность к волне в игровой форме."
				}
			}
			return "После готовности всех участников организатор запускает волну."
		}
		if w.State == "active" {
			if e.run.Scenario == "golden" && m.Tournament.State == "swiss" {
				return "Для Golden проиграйте первый раунд Swiss и выиграйте остальные. После результатов организатор завершает волну."
			}
			return "Сыграйте текущую серию. После результатов организатор завершает волну."
		}
	}
	return "Организатор сохраняет пары следующего раунда или открывает следующую стадию."
}
func (e *Engine) handle(b *Bot, err error) {
	if err == nil {
		delete(e.serverFailures, b.PlayerID)
		return
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 500:
			if e.serverFailures == nil {
				e.serverFailures = map[string]int{}
			}
			e.serverFailures[b.PlayerID]++
			if e.serverFailures[b.PlayerID] < 3 {
				e.nextTry[b.PlayerID] = time.Now().Add(5 * time.Second)
				b.Message = "Ошибка сервера, повторная проверка состояния"
				return
			}
		case 409:
			e.nextTry[b.PlayerID] = time.Now().Add(time.Second)
			b.Message = "Состояние изменилось, обновление"
			return
		case 429:
			e.nextTry[b.PlayerID] = time.Now().Add(apiErr.Retry)
			b.Message = "Ожидание лимита запросов"
			return
		case 401:
			e.logged[b.PlayerID] = false
			if c := e.clients[b.PlayerID]; c != nil {
				c.close()
			}
			e.nextTry[b.PlayerID] = time.Now().Add(10 * time.Second)
			b.Message = "Обновление сессии"
			return
		case 502, 503, 504:
			e.nextTry[b.PlayerID] = time.Now().Add(5 * time.Second)
			b.Message = "Ожидание сервера"
			return
		}
	}
	b.Status = "error"
	b.Message = "Действие остановлено: " + err.Error()
}
func (e *Engine) slot(participant string) int {
	if participant == e.run.HumanParticipantID {
		return 1
	}
	for _, b := range e.run.Bots {
		if b.ParticipantID == participant {
			return b.Slot
		}
	}
	return -1
}

//nolint:gocyclo // Check configuration, each Swiss pairing and its immutable result together.
func validateGolden(r *Run, m Metadata) error {
	if !goldenConfig(m.Configuration) {
		return userError("Настройки отличаются от сценария Golden.")
	}
	slots := map[string]int{r.HumanParticipantID: 1}
	for _, b := range r.Bots {
		slots[b.ParticipantID] = b.Slot
	}
	for _, configuration := range m.Configuration.Series {
		if configuration.Stage == "swiss" || configuration.Stage == "semifinal" {
			if configuration.Mode != "admin" || len(configuration.Categories) != 1 || configuration.Categories[0] != "crypto" {
				return userError("Категории серии отличаются от сценария Golden. Выберите Crypto без драфта.")
			}
		}
		if string(configuration.Stage) != "swiss" {
			continue
		}
		for _, series := range m.Series {
			if series.Id != configuration.Id {
				continue
			}
			first, ok := slots[series.FirstParticipantId.String()]
			second, ok2 := slots[series.SecondParticipantId.String()]
			round := int(configuration.RoundNumber)
			if !ok || !ok2 || round < 1 || round > rounds(r.Size) || first^(1<<(round-1)) != second || string(series.Format) != "bo1" {
				return userError("Пары отличаются от сценария Golden. Проверьте настройки в админке.")
			}
			if series.WinnerId != nil {
				winner := series.FirstParticipantId
				if second < first {
					winner = series.SecondParticipantId
				}
				if *series.WinnerId != winner {
					return userError("Результат отличается от сценария Golden. Для повтора создайте новый турнир.")
				}
			}
		}
	}
	return nil
}

func (e *Engine) policy(series api.Series) Policy {
	id := series.Id.String()
	if value, ok := e.run.Policies[id]; ok {
		return value
	}
	value := e.run.Next
	if series.State != api.SeriesStatePlanned && series.State != api.SeriesStateLocked && series.State != api.SeriesStateReady {
		e.run.Policies[id] = value
	}
	return value
}
func (e *Engine) answer(id uuid.UUID, version int32) (string, error) {
	answer, ok := e.tasks[taskKey(id.String(), version)]
	if !ok {
		return "", userError("Ответ для этой версии тестовой задачи отсутствует.")
	}
	return answer, nil
}
func categories(values []api.Category) []api.Category {
	result := append([]api.Category{}, values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
