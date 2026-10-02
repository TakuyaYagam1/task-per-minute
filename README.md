# Task Per Minute

[English](README.en.md)

Task Per Minute - соревновательная CTF-платформа для турниров. Участники
проходят Swiss-раунды, Golden-этап и playoffs, а backend остается источником
истины для roster, заданий, результатов, recovery evidence и проекций.

Проект создан для **RedShift**.

- Telegram-канал RedShift: [@redshift_ctf](https://t.me/redshift_ctf)

![Task Per Minute](frontend/public/task.png)

## Быстрый запуск

- Подготовьте окружение:

```bash
cp .env.example .env.local
cp .env.example .env
```

- Заполните секреты в `.env.local` для локального compose и в `.env` для
  production/server compose.

В Docker Compose backend получает `DB_DSN` из выбранного env-файла, поэтому для
контейнерного запуска DSN должен указывать на внутренний host `postgres:5432`.
`REDIS_ADDR` и `SEAWEEDFS_ENDPOINT` внутри compose задаются как
`redis:6379` и `seaweedfs:8333`; host-варианты нужны только для запуска backend
прямо с хоста:

```env
DB_DSN=postgres://admin:password@postgres:5432/task_per_minute?sslmode=disable
REDIS_ADDR=localhost:6379
SEAWEEDFS_ENDPOINT=localhost:8333
SEAWEEDFS_PUBLIC_ENDPOINT=localhost:8333
SEAWEEDFS_PUBLIC_SECURE=false
```

Для backend без контейнера временно используйте host-вариант
`postgres://admin:password@localhost:5432/task_per_minute?sslmode=disable`.

`POSTGRES_PORT`, `REDIS_PORT` и `SEAWEEDFS_*_PORT` публикуются на хост для
локальной отладки; внутри docker-сети остаются дефолтные порты контейнеров.
`SEAWEEDFS_PUBLIC_ENDPOINT` попадает в presigned URL для браузера.

- Запустите локальный compose:

```bash
cd deployment/docker
docker compose --env-file ../../.env.local -f docker-compose.local.yml up -d --build
```

Локальный compose поднимает backend и production-сборку frontend. По умолчанию
backend слушает `BACKEND_PORT=8080`, frontend слушает `FRONTEND_PORT=3000`.

Health-check:

```bash
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:3000/
```

Frontend для разработки без Docker:

```bash
cd frontend
npm install
npm run dev
```

Backend для разработки:

```bash
cd backend
go test ./...
go run ./cmd/app
```

Игрок регистрирует логин, email и пароль на `/register`, подтверждает почту
по ссылке из письма и входит на `/login`. Логин отображается на борде.
Вход только по нику закрыт: `POST /api/v1/players/join` возвращает `410`.
Настройки Resend, SMTP и перехода со старых сессий описаны в
[руководстве по почте](docs/ru/email.md).

## Сервер

[scripts/server-bootstrap.sh](scripts/server-bootstrap.sh) - это скрипт
первичной подготовки Ubuntu/Debian сервера. Он ставит Docker, Docker Compose и
git, создает runtime-пользователя, каталог приложения, `.env` и базовые firewall
rules.

Он не является deploy pipeline. Автоматический деплой выполняется через GitHub
Actions по SSH, а bootstrap нужен один раз перед первым запуском сервера.

Минимальный первый запуск после заполнения `.env`:

```bash
sudo bash scripts/server-bootstrap.sh
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env up -d --build --remove-orphans
```

Основной production compose собирает backend/frontend из исходников на сервере.
CI/CD deploy использует тот же стек с override-файлом
`deployment/docker/docker-compose.ci.yml`, где backend/frontend запускаются из
заранее собранных image-тегов.

## Документация

- [Развертывание на сервере](docs/ru/deploy.md)
- [Runbook деплоя и отката](docs/ru/runbook.md)

## Источники контракта

- [OpenAPI](backend/api/openapi.yml) - актуальный REST-контракт.
- [Развертывание](docs/ru/deploy.md) - production-конфигурация, cookie-auth,
  CSRF и WebSocket origin policy.
- [Runbook](docs/ru/runbook.md) - операционные проверки, rollback и runtime
  механики турнира.

## Команда разработки

- [CaXaRo4iK](https://github.com/CaXaRo4iK) - DevOps, деплой, инфраструктура и таски
- [FANATBEBRbl](https://github.com/FANATBEBRbl) - Frontend
- [TakuyaYagam1](https://github.com/TakuyaYagam1) - Backend

## Социальные ссылки

- RedShift Telegram: [@redshift_ctf](https://t.me/redshift_ctf)
- RedShift chat: [@redshift_ctf_chat](https://t.me/redshift_ctf_chat)

## Лицензия

Copyright (c) 2025 Task Per Minute contributors.

Проект распространяется по [GNU General Public License, версия 3](LICENSE)
(`GPL-3.0-only`). Вы можете распространять и изменять программу на условиях
этой лицензии. Программа предоставляется без каких-либо гарантий, включая
гарантии товарной пригодности и пригодности для конкретной цели.

Сторонние компоненты сохраняют собственные лицензии и уведомления об авторских правах.
