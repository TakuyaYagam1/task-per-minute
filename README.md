# Task Per Minute

[English](README.en.md)

Task Per Minute - соревновательная CTF-платформа для коротких дуэлей один на
один. Игроки подключаются, получают web-задание, решают его на скорость и
побеждают, если первыми отправляют правильный флаг.

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

## Команда разработки

- [CaXaRo4iK](https://github.com/CaXaRo4iK) - DevOps, деплой, инфраструктура и таски
- [FANATBEBRbl](https://github.com/FANATBEBRbl) - Frontend
- [TakuyaYagam1](https://github.com/TakuyaYagam1) - Backend

## Социальные ссылки

- RedShift Telegram: [@redshift_ctf](https://t.me/redshift_ctf)
- RedShift chat: [@redshift_ctf_chat](https://t.me/redshift_ctf_chat)

## Лицензия

Проект распространяется по лицензии [MIT](LICENSE).
