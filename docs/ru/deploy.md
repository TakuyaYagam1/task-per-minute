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
`/opt/task-per-minute`, настраивает UFW для `22/tcp`, `80/tcp`, `443/tcp` и
защищает `.env` правами `0600 ctf`.

Полезные переопределения:

```bash
sudo APP_USER=ctf APP_DIR=/opt/task-per-minute bash scripts/server-bootstrap.sh
sudo CONFIGURE_UFW=0 bash scripts/server-bootstrap.sh
```

## 3. Настройка окружения

Runtime `.env` лежит в корне репозитория:

```bash
sudo install -m 0600 -o ctf -g ctf .env.example .env
sudo editor .env
sudo chown ctf:ctf .env
sudo chmod 0600 .env
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
```

Если backend или migrate запускается прямо с хоста, используйте `localhost`.

## 4. Первый запуск

```bash
cd /opt/task-per-minute/deployment/docker
sudo -u ctf docker compose --env-file ../../.env run --rm migrate
sudo -u ctf docker compose --env-file ../../.env up -d --remove-orphans
```

После первого запуска Docker будет поднимать контейнеры после перезагрузки
хоста, потому что сервисы используют `restart: unless-stopped`.

## 5. Проверка

```bash
cd /opt/task-per-minute/deployment/docker
sudo -u ctf docker compose --env-file ../../.env ps
sudo -u ctf docker compose --env-file ../../.env logs -f backend
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
`DEPLOY_PATH`. Не используйте no-shell пользователя `ctf`, если специально не
меняли ему shell.

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
