# Продакшн-развертывание

Этот документ описывает первичную подготовку сервера и запуск Docker Compose
стека.

## 1. Клонирование

```bash
sudo mkdir -p /opt/task-per-minute
sudo chown "$USER:$USER" /opt/task-per-minute
git clone <repo-url> /opt/task-per-minute
cd /opt/task-per-minute
```

## 2. Подготовка сервера

На Ubuntu 22.04+ запустите bootstrap-скрипт от root:

```bash
sudo bash scripts/server-bootstrap.sh
```

Скрипт идемпотентный. Он устанавливает Docker Engine, проверяет Docker Compose,
ставит `git`, создает runtime-пользователя `ctf`, создает
`/opt/task-per-minute`, гарантирует наличие Docker-сети `proxy_tpm`,
настраивает UFW для `22/tcp`, `80/tcp`, `443/tcp` и защищает `.env`.

По умолчанию `DEPLOY_USER` берется из `SUDO_USER`: этот пользователь получает
доступ к Docker, группе `ctf`, `DEPLOY_PATH` и `.env`. Runtime-пользователь
`ctf` остается no-shell и не должен использоваться как SSH-пользователь для
GitHub Actions.

Полезные переопределения:

```bash
sudo DEPLOY_USER="$USER" APP_USER=ctf APP_DIR=/opt/task-per-minute bash scripts/server-bootstrap.sh
sudo CONFIGURE_UFW=0 bash scripts/server-bootstrap.sh
```

После добавления пользователя в группы `docker`/`ctf` перелогиньтесь, чтобы
новые группы применились в shell-сессии.

## 3. Настройка окружения

Runtime `.env` лежит в корне репозитория:

```bash
sudo install -m 0640 -o "$USER" -g ctf .env.example .env
sudo editor .env
sudo chown "$USER:ctf" .env
sudo chmod 0640 .env
```

Секреты не должны оставаться дефолтными:

```bash
openssl rand -base64 48 # JWT_SECRET
openssl rand -base64 32 # POSTGRES_PASSWORD
openssl rand -hex 24    # SEAWEEDFS_ACCESS_KEY
openssl rand -base64 48 # SEAWEEDFS_SECRET_KEY
openssl rand -base64 32 # ADMIN_PASSWORD
```

Внутри Docker Compose используйте имена сервисов:

```env
DB_DSN=postgres://<user>:<password>@postgres:5432/<db>?sslmode=disable
REDIS_ADDR=redis:6379
SEAWEEDFS_ENDPOINT=seaweedfs:8333
SEAWEEDFS_PUBLIC_ENDPOINT=files.example.com
SEAWEEDFS_PUBLIC_SECURE=true
```

`SEAWEEDFS_ENDPOINT` используется только внутри docker-сети для upload/delete и
health-check. `SEAWEEDFS_PUBLIC_ENDPOINT` указывается без схемы и должен быть
доступен браузеру через reverse proxy к SeaweedFS S3 endpoint; именно этот host
попадает в presigned ZIP URL. Если backend или migrate запускается прямо с
хоста, используйте `localhost`.

По умолчанию backend рассчитан на same-origin доступ через reverse proxy, поэтому
`HTTP_ALLOWED_ORIGINS` и `WS_ALLOWED_ORIGINS` можно оставить пустыми. Если
frontend ходит к backend напрямую с другого origin, добавьте точный allowlist
для REST и WebSocket:

```env
HTTP_ALLOWED_ORIGINS=https://app.example.com,http://localhost:3000
WS_ALLOWED_ORIGINS=https://app.example.com,http://localhost:3000
```

Если backend стоит за reverse proxy и нужен корректный IP клиента для
rate-limit и логов, добавьте CIDR адресов proxy:

```env
HTTP_TRUSTED_PROXY_CIDRS=172.18.0.0/16,127.0.0.0/8
```

## 4. Первый запуск

```bash
cd /opt/task-per-minute/deployment/docker
docker network inspect proxy_tpm >/dev/null 2>&1 || sudo docker network create proxy_tpm
docker compose --env-file ../../.env run --rm migrate
docker compose --env-file ../../.env up -d --remove-orphans
```

После первого запуска Docker будет поднимать контейнеры после перезагрузки
хоста, потому что сервисы используют `restart: unless-stopped`.

## 5. Проверка

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env ps
docker compose --env-file ../../.env logs -f backend
curl -fsS http://127.0.0.1:8080/health
```

Если сервис доступен через reverse proxy, направьте HTTP/HTTPS трафик на
backend или подключите proxy к Docker-сети `proxy_tpm`.

## 6. Деплой через GitHub Actions

Workflow деплоя находится в `.github/workflows/backend-deploy.yml`. В настройках
репозитория создайте GitHub Environment `production` и включите required
reviewers, если нужен ручной approve перед продом.

Workflow использует GitHub-hosted runner и подключается к серверу по SSH.
Self-hosted runner на продакшн-сервере для этого сценария не нужен.

Обязательные repository secrets:

```text
DEPLOY_HOST
DEPLOY_USER
DEPLOY_SSH_KEY
DEPLOY_PATH
```

Опциональные repository secrets:

```text
DEPLOY_PORT
DEPLOY_HOST_FINGERPRINT
DEPLOY_HEALTH_URL
TG_DEPLOY_WEBHOOK
SLACK_DEPLOY_WEBHOOK
```

`DEPLOY_USER` должен иметь shell, доступ к git, доступ к docker и права на
`DEPLOY_PATH`/`.env`. Bootstrap настраивает это автоматически для пользователя
из `SUDO_USER` или явно переданного `DEPLOY_USER`. Не используйте no-shell
пользователя `ctf`, если специально не меняли ему shell.

Workflow собирает SHA-tagged backend image, пушит его в GHCR, заходит на сервер
по SSH, сбрасывает репозиторий на нужный SHA и выполняет:

```bash
export BACKEND_IMAGE=<sha-image>
docker compose --env-file ../../.env pull backend migrate
docker compose --env-file ../../.env run --rm migrate
docker compose --env-file ../../.env up -d --remove-orphans backend
```

Миграции запускаются до замены backend-контейнера. Если миграция падает,
workflow останавливается, а старая версия backend продолжает работать.

Ручной откат описан в [runbook.md](runbook.md).
