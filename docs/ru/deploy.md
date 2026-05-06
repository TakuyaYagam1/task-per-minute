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
защищает `.env`.

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

В Docker Compose backend получает `DB_DSN` из `.env` напрямую. Для
контейнерного запуска DSN должен указывать на внутренний host `postgres:5432`;
`redis:6379` и `seaweedfs:8333` задаются compose как service-to-service адреса:

```env
# Нужны только для CI/CD image deploy через docker-compose.ci.yml.
# Ручной production compose собирает backend/frontend из исходников.
BACKEND_IMAGE=ghcr.io/<owner>/task-per-minute-backend:<tag>
FRONTEND_IMAGE=ghcr.io/<owner>/task-per-minute-frontend:<tag>
NGINX_IMAGE=nginx:1.30.0-alpine3.23
CERTBOT_IMAGE=certbot/certbot:v5.5.0
BACKEND_PORT=8080
FRONTEND_PORT=3000
DB_DSN=postgres://admin:<POSTGRES_PASSWORD>@postgres:5432/task_per_minute?sslmode=disable
APP_DOMAIN=example.com
ADMIN_DOMAIN=admin.example.com
API_DOMAIN=api.example.com
FILES_DOMAIN=files.example.com
CERTBOT_CERT_NAME=task-per-minute
ACME_EMAIL=admin@example.com
USE_LE_STAGING=false
SEAWEEDFS_PUBLIC_ENDPOINT=files.example.com
SEAWEEDFS_PUBLIC_SECURE=true
DOCKER_INTERNAL_SUBNET=172.30.0.0/24
HTTP_TRUSTED_PROXY_CIDRS=172.30.0.0/24
```

`POSTGRES_PORT`, `REDIS_PORT` и `SEAWEEDFS_*_PORT` публикуются на хост в local
compose; внутри docker-сети остаются дефолтные порты контейнеров. Для запуска
backend прямо с хоста временно используйте host-вариант `DB_DSN` с
`localhost:5432`; для compose оставляйте `postgres:5432`.
`SEAWEEDFS_PUBLIC_ENDPOINT` указывается без схемы и должен совпадать с
`FILES_DOMAIN`; именно этот host попадает в presigned ZIP URL, а NGINX
проксирует его на SeaweedFS S3 endpoint.

Для текущего продового разреза доменов backend открыт как
`https://api.утебяничегонеполучится.рф`, player frontend как
`https://утебяничегонеполучится.рф`, admin frontend как
`https://admin.утебяничегонеполучится.рф`. В env используйте punycode-форму:
браузерный `Origin` для IDN-домена обычно приходит именно так, а backend
сравнивает origin точной строкой.

```env
APP_DOMAIN=xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
ADMIN_DOMAIN=admin.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
API_DOMAIN=api.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
FILES_DOMAIN=files.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
HTTP_ALLOWED_ORIGINS=https://admin.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai,https://xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
WS_ALLOWED_ORIGINS=https://xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
NEXT_PUBLIC_API_URL=https://api.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
NEXT_PUBLIC_ADMIN_API_URL=https://api.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
NEXT_PUBLIC_WS_URL=wss://api.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai/ws
SEAWEEDFS_PUBLIC_ENDPOINT=files.xn--90aeebbpdxndkcm5abncn1ej9mqa.xn--p1ai
SEAWEEDFS_PUBLIC_SECURE=true
ADMIN_LOGIN_RATE_ATTEMPTS=3
ADMIN_LOGIN_RATE_WINDOW=3m
ADMIN_LOGIN_RATE_BUCKET_TTL=15m
```

Если `NEXT_PUBLIC_*` оставить пустыми, frontend будет ходить в backend через
same-origin rewrites (`/api` и `/ws`) и внутренний `BACKEND_URL`. Если значения
заданы, браузер обращается к публичному API/WS напрямую. Deploy workflow
валидирует режим сборки: либо все `NEXT_PUBLIC_*` пустые, либо заданы все три
`NEXT_PUBLIC_API_URL`, `NEXT_PUBLIC_ADMIN_API_URL`, `NEXT_PUBLIC_WS_URL`.
Прямой режим требует `https://` для REST, `wss://.../ws` для WS, общий backend
origin, а также явные GitHub vars `HTTP_ALLOWED_ORIGINS` и
`WS_ALLOWED_ORIGINS` без wildcard; WS origins должны быть подмножеством REST
origins. Эти значения вшиваются в frontend image на build-time, поэтому
изменение серверного `.env` после сборки не меняет браузерный bundle.

NGINX/compose defaults рассчитаны на backend upload до 100MB: API-capable
server blocks используют `client_max_body_size 125m`, API proxy timeout `300s`,
а backend read/write timeout `5m`.

Production compose уже содержит NGINX edge service. Наружу публикуются только
`NGINX_HTTP_PORT` и `NGINX_HTTPS_PORT`; backend, frontend, Postgres, Redis и
SeaweedFS остаются внутри Docker-сети. `expose` у внутренних сервисов не
открывает порт на host, а только документирует порт внутри compose network.

Если backend должен видеть реальный IP клиента для rate-limit и логов,
`HTTP_TRUSTED_PROXY_CIDRS` должен совпадать с внутренней Docker-сетью NGINX:

```env
DOCKER_INTERNAL_SUBNET=172.30.0.0/24
HTTP_TRUSTED_PROXY_CIDRS=172.30.0.0/24
```

## 4. Первый запуск

Перед запуском HTTPS нужны DNS `A/AAAA` записи для `APP_DOMAIN`,
`ADMIN_DOMAIN`, `API_DOMAIN` и `FILES_DOMAIN`, указывающие на сервер. NGINX
первым стартом поднимается в HTTP-only bootstrap mode, отдает
`/.well-known/acme-challenge/` из общего `certbot_webroot` volume, а `certbot`
sidecar выпускает сертификат webroot-режимом, ставит reload marker и NGINX
перезагружает полноценный HTTPS config.

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env up -d --build --remove-orphans
docker compose --env-file ../../.env logs -f nginx certbot
```

Это ручной source-build режим: backend/frontend собираются на сервере из
текущего checkout. CI/CD deploy использует тот же стек с override-файлом
`docker-compose.ci.yml`, где backend/frontend запускаются из заранее собранных
SHA-tagged images.

Для проверки DNS/firewall без риска rate-limit можно сначала поставить
`USE_LE_STAGING=true`. После staging-проверки удалите staging lineage и
перезапустите certbot с `USE_LE_STAGING=false`:

```bash
docker compose --env-file ../../.env run --rm --no-deps --entrypoint certbot certbot delete --cert-name task-per-minute
docker compose --env-file ../../.env restart certbot
```

Certbot делает 3 попытки initial issuance с паузой 60s. Если все 3 попытки
упали, контейнер не долбит Let's Encrypt бесконечно; исправьте DNS/firewall и
выполните `docker compose --env-file ../../.env restart certbot`.

После первого запуска Docker будет поднимать контейнеры после перезагрузки
хоста, потому что сервисы используют `restart: unless-stopped`.

## 5. Проверка

```bash
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env ps
docker compose --env-file ../../.env exec -T nginx nginx -t -c /tmp/nginx.conf
docker compose --env-file ../../.env exec -T nginx wget -qO- http://127.0.0.1/nginx-health
docker compose --env-file ../../.env logs --tail=100 certbot
docker compose --env-file ../../.env logs -f backend
docker compose --env-file ../../.env exec -T backend wget -qO- http://127.0.0.1:8080/health
docker compose --env-file ../../.env exec -T frontend sh -c 'wget -qO- "http://127.0.0.1:${PORT:-3000}/" >/dev/null'
```

Production compose не публикует backend/frontend порты на host. Публичный вход
идет через NGINX:

```bash
curl -fsSI "https://$APP_DOMAIN/"
curl -fsSI "https://$ADMIN_DOMAIN/admin"
curl -fsS "https://$API_DOMAIN/health"
curl -fsSI "https://$FILES_DOMAIN/"
```

Renewal идет тем же certbot sidecar через webroot challenge каждые
`CERTBOT_RENEW_INTERVAL_SECONDS` секунд. После успешного renew certbot трогает
`/var/run/certbot/reload-nginx`, а NGINX reload watcher делает hot reload без
открытия Docker socket наружу.

## 6. Деплой через GitHub Actions

Workflow деплоя backend находится в `.github/workflows/backend-deploy.yml`,
frontend - в `.github/workflows/frontend-deploy.yml`. В настройках репозитория
создайте GitHub Environment `production` и включите required reviewers, если
нужен ручной approve перед продом.

Workflow использует GitHub-hosted runner и подключается к серверу по SSH.
Self-hosted runner на продакшн-сервере для этого сценария не нужен.

Заводите эти значения в `Settings → Environments → production`, чтобы они были
ограничены продовым environment, а не доступны всему репозиторию.

Обязательные environment **secrets** (чувствительные значения):

```text
DEPLOY_HOST           # SSH-хост (IP или DNS)
DEPLOY_USER           # SSH-пользователь с доступом к docker и git
DEPLOY_SSH_KEY        # PEM приватный ключ для SSH-пользователя
DEPLOY_PATH           # абсолютный путь до деплой-чекаута на сервере
```

Опциональные environment **secrets** (тоже чувствительные, нужен только один
вебхук):

```text
TG_DEPLOY_WEBHOOK     # URL вебхука Telegram-бота для уведомлений о деплое
SLACK_DEPLOY_WEBHOOK  # URL Slack-вебхука (используется, если TG не задан)
```

Опциональные environment **variables** (`Settings → Environments → production →
Variables`, не secrets — это нечувствительные операционные значения):

Если VS Code/GitHub Actions extension показывает `Value 'production' is not
valid`, это обычно значит, что в настройках репозитория ещё не создан GitHub
Environment с именем `production` или расширение не видит настройки репозитория.
Варнинг `Context access might be invalid` для `vars.*`/`secrets.*` означает, что
соответствующий variable/secret не заведён в том scope, где job его читает.

```text
DEPLOY_PORT             # SSH-порт, по умолчанию 22
DEPLOY_HOST_FINGERPRINT # отпечаток публичного host key для строгой проверки
DEPLOY_HEALTH_URL       # обязательный публичный backend health URL, например https://api.example.com/health
DEPLOY_FRONTEND_HEALTH_URL # обязательный публичный frontend URL, например https://example.com/
```

Frontend build-time значения читает job сборки image до входа в production
environment, поэтому заведите их как repository или organization
**variables**:

```text
BACKEND_PORT            # HTTP-порт backend-контейнера для default frontend rewrites, по умолчанию 8080
FRONTEND_BACKEND_URL    # build-time BACKEND_URL для Next rewrites, по умолчанию http://backend:$BACKEND_PORT
FRONTEND_PORT           # build-time порт frontend image, по умолчанию 3000
NEXT_PUBLIC_API_URL     # публичный API URL для прямого browser-to-backend режима
NEXT_PUBLIC_ADMIN_API_URL # публичный admin API URL
NEXT_PUBLIC_WS_URL      # публичный WS URL
HTTP_ALLOWED_ORIGINS    # REST browser origins; обязательно для прямого режима
WS_ALLOWED_ORIGINS      # WS browser origins; subset HTTP_ALLOWED_ORIGINS
```

Если оставить эти значения как `secrets.*` вместо `vars.*`, GitHub Actions либо
не отдаст их нужному job, либо расширение GitHub Actions для VS Code будет
показывать варнинги «Context access might be invalid».

`DEPLOY_USER` должен иметь shell, доступ к git, доступ к docker и права на
`DEPLOY_PATH`/`.env`. Bootstrap настраивает это автоматически для пользователя
из `SUDO_USER` или явно переданного `DEPLOY_USER`. Не используйте no-shell
пользователя `ctf`, если специально не меняли ему shell.

Backend workflow собирает immutable SHA-tagged backend image, пушит его в GHCR,
заходит на сервер по SSH, сбрасывает репозиторий на нужный SHA и выполняет:

```bash
export BACKEND_IMAGE=<sha-image>
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml pull backend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans backend
```

Backend запускает Goose-миграции при старте. Если миграция падает, новый
backend-контейнер завершается или остается unhealthy, health gate не проходит,
а workflow откатывает backend на предыдущий image. После успешного health gate
workflow закрепляет `BACKEND_IMAGE` в серверном `.env` на SHA image деплоя.

Frontend workflow собирает immutable SHA-tagged frontend image, пушит его в
GHCR и обновляет только frontend service. Перед Docker build он заново
генерирует frontend OpenAPI types из backend spec в том же target SHA:

```bash
export FRONTEND_IMAGE=<sha-image>
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml pull frontend
docker compose --env-file ../../.env -f docker-compose.yml -f docker-compose.ci.yml up -d --remove-orphans frontend
```

После deploy workflow проверяет и frontend page, и same-origin rewrite
`/api/v1/leaderboard`, поэтому сломанный baked `BACKEND_URL` валит frontend
rollout, а не проходит только за счет статического ответа страницы. После
успешного health gate workflow закрепляет `FRONTEND_IMAGE` в серверном `.env`
на SHA image деплоя.

Ручной откат описан в [runbook.md](runbook.md).
