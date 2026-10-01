# Проверка удаления аккаунта

Дата: 2026-09-30. Ветка: `dev`. Проверка относится к локальному стенду.
Ранее начатые изменения регистрации и подтверждения email сохранены.

## Evidence

- Удаление отзывает сессию и удаляет учетные данные в общей транзакции:
  `backend/internal/adapter/outbound/postgres/player/player_postgres.go`.
- Миграция `backend/db/migrations/000036_player_account_deletion.sql`
  очищает остатки прежних удалений. Исторические записи игроков сохраняются.
- SQLC и OpenAPI пересозданы штатными генераторами.
- PostgreSQL integration tests и полный браузерный сценарий подтвердили
  удаление через admin API/UI, уведомление игрока и новую регистрацию.
- Локальный стенд после обновления: миграция 36, 0 аккаунтов у удаленных
  игроков, 0 пустых резервов логина, 0 ручных настроек рейтинга удаленных
  игроков. Исторические записи двух удаленных игроков сохранены.
- `/register` на порту 3000 и `/health` на порту 8080 возвращают HTTP 200.
- Образы запущенных контейнеров:
  backend `sha256:17c87c34ec2d96fb65d715b90d6d0c945390abb660a595d0cadb9ae4919c97cc`,
  frontend `sha256:c8eda368d4cba7ceaf4b68536b9992e41d0e6cb5c24b0fbd71e3c3d68530b9b0`.
- Production не изменялся в рамках этой правки. Реальные письма для проверки
  не отправлялись; браузерный тест использовал почтовую заглушку.

## Changed Files

Пути ниже указаны относительно корня проекта. Часть файлов уже содержала
изменения регистрации до этой задачи; они сохранены, а не переписаны.

- `backend/db/migrations/000036_player_account_deletion.sql`: очистка прежних удалений и хеши отозванных сессий.
- `backend/db/queries/accounts.sql`, `backend/db/queries/players.sql`: запросы удаления, освобождения логина и проверки отозванной сессии.
- `backend/internal/adapter/outbound/postgres/account/account_postgres.go`: порядок блокировок регистрации.
- `backend/internal/adapter/outbound/postgres/player/player_postgres.go`: транзакционное удаление и проверка сессии.
- `backend/internal/adapter/outbound/postgres/sqlc/accounts.sql.go`, `backend/internal/adapter/outbound/postgres/sqlc/players.sql.go`, `backend/internal/adapter/outbound/postgres/sqlc/models.go`, `backend/internal/adapter/outbound/postgres/sqlc/querier.go`: генерация SQLC.
- `backend/internal/domain/player_account.go`: ошибка удаленного аккаунта.
- `backend/internal/usecase/player/session.go`, `backend/internal/usecase/player/session_test.go`: идемпотентный logout удаленной сессии.
- `backend/internal/adapter/inbound/http/errmap/errmap.go`, `backend/internal/adapter/inbound/http/errmap/errmap_test.go`: отображение ошибки в HTTP 401.
- `backend/internal/adapter/inbound/http/middleware/error_response.go`, `backend/internal/adapter/inbound/http/middleware/player_session.go`, `backend/internal/adapter/inbound/http/middleware/player_session_test.go`: код ошибки и очистка cookies.
- `backend/internal/adapter/inbound/http/v1/player.go`, `backend/internal/adapter/inbound/http/v1/player_test.go`: удаление cookies при гонке удаления с запросом `/me`.
- `backend/api/routes/player.yml`, `backend/api/routes/player_management.yml`, `backend/api/openapi.yml`: публичный контракт удаления.
- `backend/internal/adapter/inbound/http/api/server.gen.go`, `backend/internal/adapter/inbound/http/api/spec.gen.go`, `backend/internal/adapter/inbound/http/api/types.gen.go`, `frontend/lib/shared/api/schema.ts`: генерация OpenAPI.
- `backend/integration_test/player/account_lifecycle_test.go`, `backend/integration_test/player/account_migration_test.go`, `backend/integration_test/player/player_postgres_test.go`: PostgreSQL, миграция и повторная регистрация.
- `backend/integration_test/accountflow/accountflow_test.go`, `backend/integration_test/accountflow/fixture.go`: реальный admin DELETE, rollback и сессия.
- `frontend/lib/shared/api/client.ts`, `frontend/lib/entities/player/model.ts`: очистка локальной сессии, уведомление и защита от устаревших ответов.
- `frontend/lib/widgets/player-session-guard/PlayerSessionGuard.tsx`, `frontend/lib/widgets/player-session-guard/index.ts`, `frontend/lib/widgets/index.ts`, `frontend/app/layout.tsx`: проверка сессии и модальное окно.
- `frontend/lib/pages/arena/AdminPage.tsx`: описание последствий удаления.
- `frontend/e2e/player-home-contract.spec.ts`, `frontend/e2e/account-full-stack.spec.ts`: браузерные сценарии удаления и повторной регистрации.
- `frontend/e2e/admin-contract.spec.ts`: актуальный фильтр игроков и изоляция тестов от реального сервера.
- `docs/ru/email.md`, `docs/en/email.md`: поведение удаления и порядок обновления.
- `docs/ru/account-deletion-check.md`: результаты проверки.

Остальные наблюдавшиеся изменения auth-форм, валидации паролей, resend,
CSRF, регистрации и `deployment/docker/docker-compose.local.test.yml`
относятся к предыдущим этапам. Конфликтов владения файлами не обнаружено.

## Commands Run

Команды Go выполнялись с установленным Go 1.26.8, `GOTOOLCHAIN=local`,
`GOPROXY=off`, `GOSUMDB=off`, в очищенном окружении. Интеграционные проверки
использовали отдельный PostgreSQL через локальный Podman. Секреты в команды
не подставлялись; Compose читал ранее настроенный `.env.local`.

| Каталог | Команда | Код | Результат |
| --- | --- | --- | --- |
| backend | `bash scripts/openapi-generate.sh` | 0 | Генерация и проверка контракта |
| backend | `go test -race ./...` | 0 | Unit tests |
| backend | `go tool golangci-lint run ./...` | 0 | 0 замечаний |
| backend | `.venv-quality/bin/python -m sqlfluff lint --config .sqlfluff db/migrations db/queries` | 0 | SQL lint |
| backend | `go test -race -tags=integration -count=1 ./integration_test/player` | 0 | Удаление, миграция, повторная регистрация |
| backend | `go test -race -tags=integration,account_e2e -count=1 ./integration_test/accountflow` | 0 | HTTP, cookies, rollback |
| frontend | `npm run typecheck` | 0 | TypeScript |
| frontend | `npm run lint` | 0 | ESLint и FSD |
| frontend | `playwright test e2e/player-home-contract.spec.ts --workers=1` | 0 | 10 тестов |
| frontend | `playwright test e2e/admin-contract.spec.ts --grep 'admin players section updates and deletes player stats' --workers=1` | 0 | Удаление игрока, фильтр и аудит; финальный повтор после правки заглушек |
| frontend | `E2E_ACCOUNT_FULL_STACK=1 E2E_FRONTEND_PORT=3101 npm run test:e2e -- e2e/account-full-stack.spec.ts --workers=1` | 0 | Регистрация, письмо, подтверждение, вход, admin UI DELETE, модалка, повторная регистрация |
| deployment/docker | `docker compose --env-file ../../.env.local -f docker-compose.local.test.yml build backend frontend` | 0 | Образы собирались отдельными вызовами |
| deployment/docker | `docker compose --env-file ../../.env.local -f docker-compose.local.test.yml stop backend` | 0 | Старый backend остановлен до миграции |
| deployment/docker | `docker compose --env-file ../../.env.local -f docker-compose.local.test.yml up -d --no-deps backend frontend` | 0 | Стенд обновлен, volumes сохранены |
| корень | `gitleaks stdin --redact --no-banner --no-color` | 0 | Diff кода, тестов и документации, включая новые файлы: секретов не найдено |
| корень | `git diff --check` | 0 | Формат diff |

## Validation Result

- Учетные данные, резерв исходного логина и ручные настройки рейтинга очищаются: PASS.
- Ошибка удаления при активном участии откатывает все изменения: PASS.
- Старый cookie получает `401 player.account_deleted`, cookies очищаются: PASS.
- Неизвестный или истекший токен не выдает ложное сообщение об удалении: PASS.
- Сбой сети и устаревший ответ не сбрасывают новую сессию: PASS.
- Модалка и переход на вход после подтверждения: PASS.
- Повторная регистрация с теми же email и ником создает новый ID: PASS.
- Очистка прежних удалений при сохранении активных аккаунтов и истории: PASS.
- Расширенный запуск `admin-contract.spec.ts` вместе с
  `player-home-contract.spec.ts`: 34 PASS, 6 FAIL. Тест удаления игрока и
  все 10 тестов главной страницы прошли. Шесть падений относятся к заглушкам
  потока событий админки и ожиданиям количества refresh-запросов: код уже
  использует `/api/v1/admin/events`, а старые тесты рассчитаны на
  `/api/v1/admin/players/events`. Это отдельная работа по обновлению тестов;
  весь набор админки не объявляется успешным.

## Skipped Checks

- Реальная доставка Resend: не относится к удалению аккаунта; почтовый адаптер
  не изменялся. При ручной регистрации доставку проверяет владелец стенда.
- Production deployment: текущая правка проверялась и запускалась локально.
- Повторный сетевой аудит зависимостей: manifests и lock-файлы не менялись.
  Существующие release gates остаются обязательными перед публикацией.

## Residual Risks

- История соревнований и аудит сохраняются; это не полное удаление всех
  исторических записей игрока из базы.
- Миграция очистки необратима. При ошибке нужен следующий исправляющий migration.
- Истекшие хеши отозванных сессий игнорируются при входе, физически удаляются
  пакетами при последующих удалениях игроков.
- Уведомление требует соединения с сервером. Активная вкладка проверяет сессию
  каждые 10 секунд и при возвращении фокуса.
- Шесть указанных контрактных тестов сессии админки остаются неуспешными.
  Реальный full-stack сценарий удаления и повторной регистрации прошел.

## Next Best Action

Повторить регистрацию на локальном стенде с ранее освобожденными почтой и ником.
