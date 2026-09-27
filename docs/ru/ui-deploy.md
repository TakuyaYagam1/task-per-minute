# Production UI deploy

Дата: 2026-09-27

Target: `ctfleague.ru`, `admin.ctfleague.ru`

Source: `6d4326f0fe5584f00148a35fb664a3822c4b0e8f`

## Evidence

- Production checkout: `/opt/task-per-minute`.
- Backend image до deploy: `sha256:5cf05781ae037bd30791b9e7869e5dfe9a0bb343a8305ae5e34c27cff2d19ee0`.
- Frontend image до deploy: `sha256:769e984c34d1f2ef8d874b86b6aa84003b1b5fb8f009fbef47dd110b900af5a4`.
- Backend image после deploy: `sha256:a7ef8a8f99f28f6c7e7220a9af778fa77bb2afdc0b7995b0ecfd691c02d380c5`.
- Frontend image после deploy: `sha256:9b1823e49cc3e826173e71dfbbf3a827e17e11045936b2077c8189d0d4fb84a9`.
- SHA tags `:6d4326f` и `:latest` установлены. Rollback tags `:rollback-176f288e` сохранены.
- Сборка обоих OCI образов включает SBOM и provenance. Отсутствующие blobs attestations из multi-export получены по digest; SHA-256 и размеры проверены для 17 frontend и 14 backend blobs. Стандартный frontend verifier прошел.
- Локальные release artifacts: `/home/takuya/Desktop/task-per-minute/.codex/.tmp/competition-ui/release/`.

## Changed Files

- В рамках handoff добавлен этот отчет: `docs/ru/ui-deploy.md`.
- Production code fast-forward выполнен через проверенный Git bundle на указанный source SHA. Чужие docs, untracked files и env сохранены.

## Commands Run

- Из `/opt/task-per-minute/deployment/docker`: `docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.release.yml up -d --no-deps --no-build --pull never --wait --wait-timeout 120 frontend backend`.
- Команда завершилась с exit 0.
- Rollback procedure при необходимости:

  ```sh
  cd /opt/task-per-minute/deployment/docker
  docker image tag task-per-minute-backend:rollback-176f288e task-per-minute-backend:latest
  docker image tag task-per-minute-frontend:rollback-176f288e task-per-minute-frontend:latest
  docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.release.yml up -d --no-deps --no-build --pull never --wait --wait-timeout 120 frontend backend
  ```

## Validation Result

- Backend, frontend и caddy healthy; restart count равен 0.
- Backend `/health` подтвердил готовность зависимостей и modules, schema version `29`.
- Detached frontend preview exact image прошел `check-frontend-runtime` и CSP checks.
- Production: home, arena и leaderboard вернули 200 с новыми текстами; `ctfleague.ru/admin` вернул 404; `admin.ctfleague.ru/admin` вернул 200. Leaderboard JSON через same-origin API и `/health` на API домене валидны.
- В браузере подтверждены каталог, страница соревнования с матч-центром и форма входа администратора после загрузки клиентского приложения.
- Изолированный PostgreSQL 18 race integration для времени сдачи флага прошел за 261.925 s. Первый запуск превысил лимит 3 min при работе через WAN; повтор с лимитом 8 min прошел.
- Ранее прошли 418 local UI checks, build, typecheck и lint. Текущие gitleaks, npm audit и govulncheck прошли.
- Frontend OCI validate и exact digest scan прошли, HIGH/CRITICAL vulnerabilities: 0. Backend Trivy HIGH/CRITICAL: 0.
- Миграций нет. Rollback во время deploy не потребовался.
- Временные PostgreSQL и frontend preview контейнеры, SSH tunnels и удаленный Git bundle удалены. Финальная проверка frontend и backend: healthy, restart count 0.

## Skipped Checks

- GHCR publication, protected CI publication и signing не выполнялись. Ручной OCI build остается unsigned.
- Live auth CRUD не изменялся и не проверялся; production data не записывались.

## Residual Risks

- Релиз подтвержден локальными OCI и production health checks, но hosted GHCR/protected CI signing evidence отсутствует.
- Первый integration timeout был сетевым; успешный повторный прогон завершен.
- Действия внутри авторизованной админки на production не проверялись.
- Сохраненные в базе названия демо-соревнований могут содержать прежние слова. Их переименование требует отдельного изменения данных.

## Next Best Action

- Выкладка завершена. При необходимости использовать сохраненные rollback tags и процедуру выше.
