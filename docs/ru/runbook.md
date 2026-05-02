# Runbook деплоя

Операционные заметки для продакшн-деплоя и ручного отката.

## Обычный деплой

GitHub Actions workflow собирает SHA-tagged backend image, пушит его в GHCR,
заходит на сервер по SSH, сбрасывает репозиторий на целевой SHA и выполняет:

```bash
cd /opt/task-per-minute/deployment/docker
export BACKEND_IMAGE=<sha-image>
docker network inspect proxy_tpm >/dev/null 2>&1 || docker network create proxy_tpm
docker compose --env-file ../../.env pull backend migrate
docker compose --env-file ../../.env run --rm migrate
docker compose --env-file ../../.env up -d --remove-orphans backend
```

Миграции выполняются до замены backend-контейнера. Если миграция падает,
workflow останавливается, а старая версия backend продолжает работать.

## Health gate

Деплой считается успешным только когда `/health` возвращает все зависимости в
состоянии `ok` и положительную версию схемы:

```bash
curl -fsS http://127.0.0.1:8080/health | jq .
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

## Откат backend image

Используйте это, если новый контейнер стартовал, но health-check не прошел.
Workflow делает такой откат автоматически через `.last-deployed-sha`; ниже
ручные команды.

```bash
cd /opt/task-per-minute

IMAGE_REPO=ghcr.io/<owner>/task-per-minute-backend
PREVIOUS_SHA="$(tr -d '[:space:]' < .last-deployed-sha)"
PREVIOUS_IMAGE="$IMAGE_REPO:$PREVIOUS_SHA"

git fetch --prune origin
git reset --hard "$PREVIOUS_SHA"

cd deployment/docker
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env pull backend
docker compose --env-file ../../.env up -d --remove-orphans backend
curl -fsS http://127.0.0.1:8080/health | jq .
```

## Рестарт стека

Для обычного рестарта без удаления данных:

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env restart backend
```

Для пересоздания backend-контейнера на текущем image:

```bash
docker compose --env-file ../../.env up -d --remove-orphans backend
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
docker compose --env-file ../../.env run --rm migrate status
```

Откатить одну миграцию:

```bash
export BACKEND_IMAGE="$PREVIOUS_IMAGE"
docker compose --env-file ../../.env run --rm migrate down
```

Повторяйте `migrate down` только после отдельной проверки каждого шага. После
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
