# Runbook деплоя

Операционные заметки для продакшн-деплоя и ручного отката.

## Обычный деплой

GitHub Actions workflow собирает immutable SHA-tagged backend/frontend images,
пушит их в GHCR, заходит на сервер по SSH, сбрасывает репозиторий на целевой
SHA и выполняет:

```bash
cd /opt/task-per-minute/deployment/docker
export BACKEND_IMAGE=<sha-image>
export FRONTEND_IMAGE=<sha-image>
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml pull backend frontend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans backend frontend
```

Backend запускает Goose-миграции при старте. Если миграция падает, новый
backend-контейнер завершается или остается unhealthy, health gate не проходит,
а workflow откатывает backend/frontend на предыдущие images и проверяет health.
Успешные deploy и rollback шаги также обновляют `BACKEND_IMAGE` и
`FRONTEND_IMAGE` в серверном `.env`, чтобы последующие ручные
compose-операции использовали pinned images.

## Health gate

Деплой считается успешным только когда `/health` возвращает все зависимости в
состоянии `ok` и положительную версию схемы:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env exec -T backend wget -qO- http://127.0.0.1:8080/health | jq .
```

Ожидаемые поля:

```json
{
  "status": "ok",
  "db": "ok",
  "redis": "ok",
  "seaweedfs": "ok",
  "schema_version": 2
}
```

Frontend health gate проверяет, что frontend URL отвечает успешным HTTP
статусом, а same-origin API rewrite возвращает ожидаемую форму leaderboard:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env exec -T frontend sh -c 'wget -qO- "http://127.0.0.1:${PORT:-3000}/" >/dev/null'
docker compose --env-file ../../.env exec -T frontend sh -c 'wget -qO- "http://127.0.0.1:${PORT:-3000}/api/v1/leaderboard"' | jq -e '.entries | type == "array"'
docker compose --env-file ../../.env exec -T caddy caddy validate --config /etc/caddy/Caddyfile
docker compose --env-file ../../.env exec -T caddy wget -qO- http://127.0.0.1:2019/config/ >/dev/null
docker compose --env-file ../../.env logs --tail=100 caddy
```

## Быстрый troubleshooting Auth/WS

Если WebSocket не подключается:

- Проверьте browser DevTools: URL должен быть `/ws` или `wss://<api>/ws` без
  `?token=...`.
- Проверьте `WS_ALLOWED_ORIGINS`: origin player frontend должен совпадать
  точной строкой, включая punycode для IDN.
- Если включен `WS_REQUIRE_ORIGIN=true`, убедитесь, что клиент действительно
  отправляет browser `Origin`; CLI/скриптовые клиенты без Origin будут получать
  `403`.
- Проверьте, что player join/me выдают cookie `tpm_player_session`, а браузер
  отправляет ее на `/ws`.
- Backend должен вернуть `401/403/429` как `application/problem+json` до
  upgrade, если сессии нет, origin запрещен или handshake rate-limit исчерпан.

Если unsafe REST запросы получают `403 csrf token invalid`:

- Для player cookie-auth проверьте `tpm_player_csrf` и header `X-CSRF-Token`.
- Для admin mutations проверьте access CSRF token в `X-CSRF-Token`.
- Для admin refresh/logout проверьте refresh CSRF token из
  `X-Admin-Refresh-CSRF-Token`; отправлять его можно в `X-CSRF-Token` или
  `X-Admin-Refresh-CSRF-Token`. Access CSRF token для этих endpoints не
  подходит.
- После logout frontend должен очистить marker сессии и CSRF tokens; повторный
  login должен получить новые CSRF headers.

Если все пользователи получают `429` за Caddy:

- Проверьте `HTTP_TRUSTED_PROXY_CIDRS` против реальной compose-сети:
  `docker network inspect task-per-minute_internal`.
- Сравните backend `client_ip` в security logs с Caddy logs.
- Убедитесь, что Caddy передает `X-Forwarded-For`, а backend доверяет только
  CIDR самого proxy, не всему интернету.

## Откат backend image

Используйте это, если новый контейнер стартовал, но health-check не прошел.
Workflow делает такой откат автоматически через `.last-deployed-sha`; ниже
ручные команды. Если `.last-deployed-sha` отсутствует, автоматический rollback
недоступен и deploy требует ручного recovery.

```bash
cd /opt/task-per-minute

IMAGE_REPO=ghcr.io/<owner>/task-per-minute-backend
PREVIOUS_SHA="$(tr -d '[:space:]' < .last-deployed-sha)"
PREVIOUS_IMAGE="$IMAGE_REPO:$PREVIOUS_SHA"

git fetch --prune origin
git reset --hard "$PREVIOUS_SHA"

cd deployment/docker
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml pull backend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans backend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml exec -T backend wget -qO- http://127.0.0.1:8080/health | jq .
```

## Откат frontend image

Используйте это, если новый frontend image развернулся, но frontend health gate
не прошел. Workflow делает такой откат через
`.last-deployed-frontend-sha` и после rollback проверяет frontend page плюс
same-origin API rewrite. Если `.last-deployed-frontend-sha` отсутствует,
автоматический rollback недоступен и deploy требует ручного recovery. Успешные
deploy и rollback шаги также обновляют `FRONTEND_IMAGE` в серверном `.env`,
чтобы последующие ручные compose-операции использовали pinned image.

```bash
cd /opt/task-per-minute

IMAGE_REPO=ghcr.io/<owner>/task-per-minute-frontend
PREVIOUS_SHA="$(tr -d '[:space:]' < .last-deployed-frontend-sha)"
PREVIOUS_IMAGE="$IMAGE_REPO:$PREVIOUS_SHA"

git fetch --prune origin
git reset --hard "$PREVIOUS_SHA"

cd deployment/docker
export FRONTEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml pull frontend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans frontend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml exec -T frontend sh -c 'wget -qO- "http://127.0.0.1:${PORT:-3000}/" >/dev/null'
```

## Рестарт стека

Для обычного рестарта без удаления данных:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env restart backend frontend
```

Для пересоздания backend/frontend-контейнеров из текущего checkout:

```bash
docker compose --env-file ../../.env up -d --build --remove-orphans backend frontend
```

Если стек был развернут через CI/CD images, используйте image override:

```bash
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans backend frontend
```

## Полное удаление стека

Обычная остановка без удаления данных:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env down --remove-orphans
```

Полное удаление с volume-данными базы, Redis и SeaweedFS:

```bash
docker compose --env-file ../../.env down -v --remove-orphans
```

Команда с `-v` удаляет данные. Используйте ее только для полного сброса
окружения или после backup.

## Ручной откат миграции

Продакшн-деплой не запускает `goose down` автоматически. Используйте это только
после проверки, что у миграции есть корректный `-- +goose Down`, откат совместим
с backend image, который вы возвращаете, и перед этим есть свежий backup базы.

Проверить статус:

```bash
cd /opt/task-per-minute/deployment/docker
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml run --rm --entrypoint /app/migrate backend status
```

Откатить одну миграцию:

```bash
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml run --rm --entrypoint /app/migrate backend down
```

Повторяйте `/app/migrate down` только после отдельной проверки каждого шага. После
отката схемы задеплойте совместимый backend image и проверьте `/health`.

## Механика подсказок

В каждой дуэли таск содержит ровно 3 подсказки. Бэкенд автоматически
отправляет их обоим игрокам через WebSocket-событие `hint_unlocked` на
25 %, 50 % и 75 % от `time_limit` таска (см. `domain.BuildHintSchedule`).

- Подсказки **не влияют на очки** и не «покупаются» вручную: разблокировка
  происходит по таймеру, выровненному относительно `started_at`.
- При паузе таймера (`opponent_disconnected`) расписание подсказок
  замораживается вместе с дедлайном дуэли и продолжается после `duel_resume`.
- При повторном подключении игрока бэкенд высылает уже разблокированные
  подсказки в составе `duel_resume`, поэтому ни одна подсказка не теряется.
- E2E покрытие: `TestE2EHintFlow_AutoUnlocksAt25_50_75` в
  `backend/integration_test/e2e_test.go` поднимает реальный backend и
  проверяет порядок и текст всех трёх событий.

## Механика reconnect

- Разрыв WebSocket во время активной дуэли переводит дуэль в reconnect-паузу:
  дедлайн дуэли и расписание подсказок замораживаются, а соперник получает
  `opponent_disconnected`.
- Если игрок возвращается внутри reconnect-window, сервер отправляет ему
  `duel_resume`, сопернику - `opponent_reconnected`, после чего дедлайн и
  подсказки продолжаются с учётом времени паузы.
- Если reconnect-window истёк, дуэль завершается ничьей. Превышение лимита
  disconnect/reconnect для игрока тоже немедленно завершает дуэль ничьей.
- Если оба участника отключились и оба reconnect-window истекли, результат
  также остаётся ничьей.
- При ничьей leaderboard не получает win; `winner_id` остаётся пустым.
