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
cp .env.example .env
```

- Заполните секреты в `.env`.

Для запуска через Docker Compose используйте сервисные имена контейнеров в
переменных, которые попадают внутрь контейнеров:

```env
DB_DSN=postgres://admin:password@postgres:5432/task_per_minute?sslmode=disable
REDIS_ADDR=redis:6379
SEAWEEDFS_ENDPOINT=seaweedfs:8333
SEAWEEDFS_PUBLIC_ENDPOINT=localhost:8333
SEAWEEDFS_PUBLIC_SECURE=false
```

`SEAWEEDFS_ENDPOINT` нужен backend-контейнеру для внутренних S3-запросов, а
`SEAWEEDFS_PUBLIC_ENDPOINT` попадает в presigned URL для браузера. Для запуска
backend прямо с хоста используйте `localhost` в обоих адресах.

- Запустите локальный compose:

```bash
cd deployment/docker
docker compose --env-file ../../.env -f docker-compose.local.yml run --rm migrate
docker compose --env-file ../../.env -f docker-compose.local.yml up -d --build
```

Backend health-check:

```bash
curl -fsS http://127.0.0.1:8080/health
```

Frontend для разработки:

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
docker compose --env-file ../../.env run --rm migrate
docker compose --env-file ../../.env up -d --remove-orphans
```

## Документация

- [Развертывание на сервере](docs/ru/deploy.md)
- [Runbook деплоя и отката](docs/ru/runbook.md)

## Команда разработки

- [CaXaRo4iK](https://github.com/CaXaRo4iK) - DevOps, деплой, инфраструктура и таски
- [FANATBEBRbl](https://github.com/FANATBEBRbl) - Frontend
- [skr1ms](https://github.com/skr1ms) - Backend

## Социальные ссылки

- RedShift Telegram: [@redshift_ctf](https://t.me/redshift_ctf)
- RedShift chat: [@redshift_ctf_chat](https://t.me/redshift_ctf_chat)

## Лицензия

Проект распространяется по лицензии [MIT](LICENSE).
