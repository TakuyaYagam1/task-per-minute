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
  "schema_version": 11
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

- Проверьте browser DevTools: URL должен быть одним из role-scoped tournament
  endpoints и не должен содержать credentials в query string:
  - public: `/api/v1/tournaments/{tournament_id}/realtime`;
  - participant: `/api/v1/tournaments/{tournament_id}/participant/realtime`;
  - operator: `/api/v1/admin/tournaments/{tournament_id}/realtime`.
- Проверьте `WS_ALLOWED_ORIGINS`: origin player frontend должен совпадать
  точной строкой, включая punycode для IDN.
- Если включен `WS_REQUIRE_ORIGIN=true`, убедитесь, что клиент действительно
  отправляет browser `Origin`; CLI/скриптовые клиенты без Origin будут получать
  `403`.
- Проверьте, что player join/me выдают cookie `tpm_player_session`, а браузер
  отправляет ее на participant endpoint. Operator endpoint требует cookie
  `tpm_admin_access`. Public endpoint не требует аутентификации.
- Backend должен вернуть `401/403/429` как `application/problem+json` до
  upgrade, если сессии нет, origin запрещен или handshake rate-limit исчерпан.
- `503` до upgrade с detail `websocket capacity reached` означает, что общий
  `WS_MAX_CONNECTIONS` исчерпан. `429` при валидной session также может
  означать, что authenticated participant или operator достиг
  `WS_MAX_CONNECTIONS_PER_PRINCIPAL`.

Если unsafe REST запросы получают `403 csrf token invalid`:

- Для player cookie-auth проверьте `tpm_player_csrf` и header `X-CSRF-Token`.
- Для admin mutations проверьте access CSRF token в `X-CSRF-Token`.
- Для admin refresh/logout проверьте refresh CSRF token из
  response header `X-Admin-Refresh-CSRF-Token`, затем отправьте его значение в
  `X-CSRF-Token`. `X-Admin-Refresh-CSRF-Token` не принимается как request
  header. Access CSRF token для этих endpoints не подходит.
- После logout frontend должен очистить CSRF tokens в памяти и сбросить
  authenticated UI state; повторный login должен получить новые CSRF headers.

Если player join возвращает `409`, username все еще связан с активной session.
Используйте другой username или дождитесь истечения текущей session. Знания
публичного username недостаточно для ее замены.

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

## Tournament realtime

Каждое подключение получает role-scoped snapshot, собранный из канонического
состояния PostgreSQL. Public snapshot содержит только tournament, scoreboard,
bracket, series, draft и official result projections. Participant snapshot
добавляет только assignment, task и текущего opponent этого игрока. Operator
snapshot содержит operational wave, readiness, presence, pause, replay и audit
links.

Текущий protocol основан на snapshot. При reconnect клиент снова открывает тот
же role endpoint и получает свежий snapshot. Клиент не должен самостоятельно
додумывать пропущенные events, identities, timers или results.
