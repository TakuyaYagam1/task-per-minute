# Изменения интерфейса соревнований

Дата: 2026-09-27. Рабочая ветка: `dev`. Базовый коммит: `176f288e93b7773ce66118dcc6983ecc56e3296b`.

## Evidence

- Убраны лишние описания и дублирующиеся заголовки на главной, в каталоге, на странице соревнования и в админке.
- Пользовательские названия этапов приведены к квалификации и плей-офф. Элементы дополнительного отбора названы по их назначению.
- Закрепленная шапка объединяет логотип, тему и выход в админке. Общая круглая кнопка "Наверх" появляется после прокрутки.
- Список задач занимает два столбца на широком экране и один на телефоне. Создание и редактирование открываются в окне с возвратом фокуса и защитой несохраненных изменений.
- Возврат к списку соревнований оформлен кнопкой; рейтинг получил переход в Arena.
- В трансляции убраны обычные индикаторы соединения и технические поля игры. BO1 и BO3 подписаны явно. Для принятого ответа показывается время решения победителя.
- Время решения вычисляется из принятого ответа, связанного с текущей официальной версией результата. Нулевое время сохраняется; отсутствие данных не подменяется длительностью до публикации результата.
- Код интерфейса просмотрен координатором. Снимки главной, каталога, списка задач, окна редактирования, входа администратора и карточки результата просмотрены в светлой и темной темах, на широком и узком экранах.
- Новый необязательный `solve_time_ms` проходит через OpenAPI, SQL, REST, WS и frontend. Сгенерированные файлы обновлены штатными генераторами.

## Changed Files

Все перечисленные пути принадлежат этой задаче. Замеченное стороннее изменение `.env.local` сохранено; содержимое файла не читалось. Изменения пользовательских названий соревнований в базе не выполнялись.

### Backend: время принятого ответа, REST/WS, SQL и сгенерированные контракты

- `backend/api/components/schemas/recovery_schemas.yml`
- `backend/db/queries/tournament_read.sql`
- `backend/integration_test/tournament_public_live_projection_test.go`
- `backend/internal/adapter/inbound/http/api/spec.gen.go`
- `backend/internal/adapter/inbound/http/api/types.gen.go`
- `backend/internal/adapter/inbound/http/v1/tournament_public.go`
- `backend/internal/adapter/inbound/websocket/tournament/public_snapshot.go`
- `backend/internal/adapter/inbound/websocket/tournament/public_snapshot_test.go`
- `backend/internal/adapter/inbound/websocket/tournament_source.go`
- `backend/internal/adapter/outbound/postgres/sqlc/tournament_read.sql.go`
- `backend/internal/adapter/outbound/postgres/tournament/snapshot/tournament_snapshot_public.go`
- `backend/internal/adapter/outbound/postgres/tournament/snapshot/tournament_snapshot_test.go`
- `backend/internal/port/inbound/contract_tournament_snapshot.go`

### Frontend: страницы, тексты, формы, навигация и общие элементы

- `frontend/app/layout.tsx`
- `frontend/lib/entities/tournament/catalog.ts`
- `frontend/lib/features/tournament-entry/TournamentEntry.module.css`
- `frontend/lib/features/tournament-entry/TournamentEntry.tsx`
- `frontend/lib/features/tournament-live/TournamentLivePanel.tsx`
- `frontend/lib/features/tournament-operator-actions/TournamentOperatorActions.tsx`
- `frontend/lib/features/tournament-operator-actions/TournamentResultCorrection.tsx`
- `frontend/lib/features/tournament-player/model.ts`
- `frontend/lib/features/tournament-player/use-participant-golden.ts`
- `frontend/lib/pages/arena/ArenaPublicTournamentPage.module.css`
- `frontend/lib/pages/arena/ArenaPublicTournamentPage.tsx`
- `frontend/lib/pages/arena/ArenaRolePage.tsx`
- `frontend/lib/pages/arena/HomePage.module.css`
- `frontend/lib/pages/arena/HomePage.tsx`
- `frontend/lib/pages/arena/LeaderboardPage.tsx`
- `frontend/lib/pages/arena/admin.module.css`
- `frontend/lib/pages/arena/admin/AdminLogin.tsx`
- `frontend/lib/pages/arena/admin/AdminShell.tsx`
- `frontend/lib/pages/arena/leaderboard.module.css`
- `frontend/lib/shared/api/schema.ts`
- `frontend/lib/shared/api/tournament-recovery.ts`
- `frontend/lib/shared/lib/tournament-format.ts`
- `frontend/lib/shared/ui/Dialog.tsx`
- `frontend/lib/shared/ui/Tabs.tsx`
- `frontend/lib/shared/ui/__fixture__/app/page.tsx`
- `frontend/lib/widgets/arena/ArenaShell.tsx`
- `frontend/lib/widgets/site-header/SiteHeader.tsx`
- `frontend/lib/widgets/tournament-admin/GoldenPlayoffControlPanel.tsx`
- `frontend/lib/widgets/tournament-admin/RosterEditor.tsx`
- `frontend/lib/widgets/tournament-admin/SeriesConfigurationEditor.tsx`
- `frontend/lib/widgets/tournament-admin/SwissPairingEditor.tsx`
- `frontend/lib/widgets/tournament-admin/TournamentAdminPanel.module.css`
- `frontend/lib/widgets/tournament-admin/TournamentAdminPanel.tsx`
- `frontend/lib/widgets/tournament-admin/TournamentAuditPanel.tsx`
- `frontend/lib/widgets/tournament-admin/TournamentContentManager.module.css`
- `frontend/lib/widgets/tournament-admin/TournamentContentManager.tsx`
- `frontend/lib/widgets/tournament-admin/TournamentJournalSection.tsx`
- `frontend/lib/widgets/tournament-admin/TournamentStartControls.tsx`
- `frontend/lib/widgets/tournament-admin/TournamentTasksSection.module.css`
- `frontend/lib/widgets/tournament-admin/TournamentTasksSection.tsx`
- `frontend/lib/widgets/tournament-admin/WaveControlPanel.tsx`
- `frontend/lib/widgets/tournament-broadcast/TournamentBroadcastPanel.module.css`
- `frontend/lib/widgets/tournament-broadcast/TournamentBroadcastPanel.tsx`
- `frontend/lib/widgets/tournament-catalog/TournamentCatalog.module.css`
- `frontend/lib/widgets/tournament-catalog/TournamentCatalog.tsx`
- `frontend/lib/widgets/tournament-player/ParticipantGoldenPanel.tsx`
- `frontend/lib/widgets/tournament-player/TournamentPlayerPanel.tsx`
- `frontend/lib/widgets/back-to-top/index.ts`
- `frontend/lib/widgets/back-to-top/BackToTop.tsx`
- `frontend/lib/widgets/back-to-top/BackToTop.module.css`

### Браузерные проверки и фикстуры для измененного интерфейса

- `frontend/e2e/admin-contract.spec.ts`
- `frontend/e2e/admin-navigation.spec.ts`
- `frontend/e2e/admin-visual.spec.ts`
- `frontend/e2e/classic-ui.spec.ts`
- `frontend/e2e/fixtures/operator-api/app/page.tsx`
- `frontend/e2e/fixtures/public-api/app/page.tsx`
- `frontend/e2e/fixtures/tournament-formatting/app/layout.tsx`
- `frontend/e2e/fixtures/tournament-formatting/app/page.tsx`
- `frontend/e2e/fixtures/tournament-live/app/page.tsx`
- `frontend/e2e/full-stack-local.spec.ts`
- `frontend/e2e/leaderboard-contract.spec.ts`
- `frontend/e2e/live-backend.spec.ts`
- `frontend/e2e/player-home-contract.spec.ts`
- `frontend/e2e/site-header.spec.ts`
- `frontend/e2e/theme-contract.spec.ts`
- `frontend/e2e/tournament-admin-audit-contract.spec.ts`
- `frontend/e2e/tournament-admin-content-contract.spec.ts`
- `frontend/e2e/tournament-admin-correction-contract.spec.ts`
- `frontend/e2e/tournament-admin-golden-playoff-contract.spec.ts`
- `frontend/e2e/tournament-admin-list-contract.spec.ts`
- `frontend/e2e/tournament-admin-pairing-contract.spec.ts`
- `frontend/e2e/tournament-admin-recovery-contract.spec.ts`
- `frontend/e2e/tournament-admin-roster-contract.spec.ts`
- `frontend/e2e/tournament-admin-runtime-controls-contract.spec.ts`
- `frontend/e2e/tournament-admin-series-configuration.spec.ts`
- `frontend/e2e/tournament-admin-wave-contract.spec.ts`
- `frontend/e2e/tournament-admission.spec.ts`
- `frontend/e2e/tournament-catalog.spec.ts`
- `frontend/e2e/tournament-entry-contract.spec.ts`
- `frontend/e2e/tournament-formatting-contract.spec.ts`
- `frontend/e2e/tournament-game-assignment-contract.spec.ts`
- `frontend/e2e/tournament-operator-contract.spec.ts`
- `frontend/e2e/tournament-participant-contract.spec.ts`
- `frontend/e2e/tournament-participant-golden-contract.spec.ts`
- `frontend/e2e/tournament-participant-submission-contract.spec.ts`
- `frontend/e2e/tournament-public-broadcast-contract.spec.ts`
- `frontend/e2e/tournament-recovery-contract.spec.ts`
- `frontend/e2e/tournament-start-contract.spec.ts`
- `frontend/e2e/tournament/demo-readiness-contract.spec.ts`
- `frontend/e2e/tournament/fixture-page.ts`
- `frontend/e2e/tournament/fixtures.spec.ts`

### Отчет

- `docs/ru/ui-changes.md`: перечень изменений и результаты проверок.

## Commands Run

Команды выполнялись из корня проекта, `frontend/` или `backend/`, как указано ниже. Для процессов использовалось очищенное окружение `env -i`; реальные учетные данные не передавались. Frontend preview использовал неработающий адрес backend `http://127.0.0.1:9`, API браузерных проверок подменялись фикстурами.

| Каталог | Команда | Результат |
| --- | --- | --- |
| frontend | `./node_modules/.bin/next build` | 0, production-сборка создана |
| frontend | `./node_modules/.bin/eslint .` | 0 |
| frontend | `./node_modules/.bin/tsc --noEmit` | 0 |
| корень | `git diff --check -- frontend backend` | 0 |
| frontend | `node scripts/check-fsd.mjs` | 0, фактические импорты и синтетические случаи |
| frontend | `E2E_SKIP_WEB_SERVER=1 E2E_PRODUCTION=1 E2E_FRONTEND_URL=http://127.0.0.1:3310 ./node_modules/.bin/playwright test <default specs> --workers=4 --reporter=line` | 0, 405 проверок |
| frontend | `E2E_SKIP_WEB_SERVER=1 E2E_PRODUCTION=1 E2E_FRONTEND_URL=http://127.0.0.1:3310 ./node_modules/.bin/playwright test e2e/admin-visual.spec.ts e2e/site-header.spec.ts e2e/tournament-admin-audit-contract.spec.ts e2e/tournament-admin-golden-playoff-contract.spec.ts --workers=2` | 0, оставшиеся 13 проверок, выполнено worker |
| frontend | `LANG=C.UTF-8 E2E_SKIP_WEB_SERVER=1 E2E_PRODUCTION=1 E2E_FRONTEND_URL=http://127.0.0.1:3310 ./node_modules/.bin/playwright test e2e/tournament-admin-audit-contract.spec.ts --workers=2 --reporter=line` | 0, 6 проверок после добавления точного имени скачиваемого отчета |
| backend | `go build ./...` | 0, Go 1.26.8 |
| backend | `go vet ./internal/adapter/inbound/http/v1 ./internal/adapter/inbound/websocket/... ./internal/adapter/outbound/postgres/tournament/snapshot` | 0 |
| backend | `go test -race ./internal/adapter/inbound/http/v1 ./internal/adapter/inbound/websocket/... ./internal/adapter/outbound/postgres/tournament/snapshot -count=1` | 0, пять пакетов |
| backend | `go test -race -tags=integration ./integration_test -run '^TestTournamentPublicLiveProjectionThroughHTTPAndRealtime$' -count=1 -timeout=3m` | 0, PostgreSQL 17.11, отдельная локальная тестовая база |
| backend | `go tool sqlc generate -f codegen/sqlc.yaml` | 0, выполнено worker |
| backend | `PYTHON=/home/takuya/Desktop/task-per-minute/backend/.venv-quality/bin/python bash scripts/openapi-generate.sh` | 0, выполнено worker |
| backend | `.venv-quality/bin/python -m sqlfluff lint --config .sqlfluff db/queries/tournament_read.sql` | 0, выполнено worker |

Go запускался из кешированного toolchain 1.26.8 с `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOWORK=off`. Интеграционный тест использовал только созданную для задачи базу на `127.0.0.1:55371` через `TPM_TEST_POSTGRES_DSN`.

Список для прогона 405 проверок получен командой `rg --files e2e -g '*.spec.ts'` с исключением четырех файлов из следующей строки таблицы и трех отдельных наборов: `full-stack-local.spec.ts`, `live-backend.spec.ts`, `release-proxy.spec.ts`.

Для кириллического имени скачиваемого отчета тестовому Chromium требуется `LANG=C.UTF-8`. Изолированный опыт с одинаковыми ссылками подтвердил: в локали `C` браузер заменяет такое имя на `download`, в `C.UTF-8` сохраняет полное имя. Поэтому итоговая проверка имени выполнялась с явной UTF-8 локалью.

## Validation Result

Все 418 браузерных проверок прошли в двух непересекающихся группах: 405 и 13. Это весь стандартный набор Playwright, включая CRUD задач, адаптивный список, закрытие окна, навигацию, время решения и обновление матча. Проверены светлая и темная темы, ширины 375, 768 и 1440 пикселей; шапка также проверена при 320 пикселях. Первый общий прогон выявил устаревшие ожидания текстов и режима CSP; они обновлены по фактическому интерфейсу, а проверки доступа, запросов и содержимого ответов сохранены.

Backend race и интеграция проверяют обычное время решения, нулевое время, несовпадение времени принятого ответа и завершения, отсутствие старого времени после исправления официального результата. Валидация нового поля отвергает некорректные значения. Новый frontend принимает ответы без этого необязательного поля.

Локальные логи и снимки: `.codex/.tmp/competition-ui/`. Основные логи: `build.log`, `lint-final.log`, `types-final.log`, `backend-race-final.log`, `projection-race-final.log`, `final-tests.log`, `admin-ui-final-4.log`, `export-utf8-tests.log`. Временные production preview и тестовая PostgreSQL остановлены после проверок.

## Skipped Checks

- Docker full-stack, PostgreSQL 18 и release-proxy: локальный пользователь не имеет доступа к сокету Docker, повышение прав требует пароль. Перед выпуском нужен прогон владельцем окружения с доступным Docker.
- Проверки живого окружения и production deploy не выполнялись: текущая задача относится к локальным изменениям интерфейса.

## Residual Risks

- Локальная интеграция выполнена на PostgreSQL 17.11; совместимость с целевым окружением PostgreSQL 18 требует отдельного full-stack прогона.
- Frontend и backend с новым полем нужно выпускать согласованно. Старый frontend со строгой проверкой ключей может отклонить новый ответ до обновления страницы.
- Сохраненные пользователем названия из базы показываются как есть, включая ранее созданные названия со старой терминологией.

## Next Best Action

Изменения готовы к просмотру в рабочей копии. Перед публикацией выполнить full-stack проверки в окружении с Docker.
