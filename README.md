# Task Per Minute

Task Per Minute - соревновательная CTF-платформа для турниров. Участники
проходят раунды по швейцарской системе, этап Golden и плей-офф. Сервер
управляет составом участников, заданиями, результатами и восстановлением игр.

Проект создан для **RedShift**.

- Telegram-канал RedShift: [@redshift_ctf](https://t.me/redshift_ctf)

![Task Per Minute](frontend/public/task.png)

## Быстрый запуск

- Подготовьте окружение:

```bash
cp .env.example .env.local
cp .env.example .env
```

- Заполните секреты в `.env.local` для локального запуска и в `.env` для сервера.

В Docker Compose backend получает `DB_DSN` из выбранного файла окружения, поэтому
для контейнерного запуска DSN должен указывать на внутренний адрес `postgres:5432`.
`REDIS_ADDR` и `SEAWEEDFS_ENDPOINT` внутри compose задаются как
`redis:6379` и `seaweedfs:8333`; адреса localhost нужны при запуске backend
непосредственно на компьютере:

```env
DB_DSN=postgres://admin:password@postgres:5432/task_per_minute?sslmode=disable
REDIS_ADDR=localhost:6379
SEAWEEDFS_ENDPOINT=localhost:8333
SEAWEEDFS_PUBLIC_ENDPOINT=localhost:8333
SEAWEEDFS_PUBLIC_SECURE=false
```

Для backend без контейнера временно используйте адрес
`postgres://admin:password@localhost:5432/task_per_minute?sslmode=disable`.

`POSTGRES_PORT`, `REDIS_PORT` и `SEAWEEDFS_*_PORT` публикуются на хост для
локальной отладки; внутри сети Docker остаются стандартные порты контейнеров.
`SEAWEEDFS_PUBLIC_ENDPOINT` используется в подписанных ссылках для браузера.

- Запустите локальный стенд:

```bash
cd deployment/docker
docker compose --env-file ../../.env.local -f docker-compose.local.yml up -d --build
```

Локальный Compose запускает backend и готовую сборку frontend. По умолчанию
backend слушает `BACKEND_PORT=8080`, frontend слушает `FRONTEND_PORT=3000`.

Проверка доступности:

```bash
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:3000/
```

Для локальной разработки на NixOS можно войти в необязательное окружение
командой `nix-shell shell.nix` из корня репозитория. CI и Docker его не используют.
После установки npm-зависимостей браузер для тестов устанавливается из папки
`frontend` командой `npx --no-install playwright install chromium`.

Разработка интерфейса без Docker:

```bash
cd frontend
npm install
npm run dev
```

Разработка серверной части (Go 1.26.8):

```bash
cd backend
make mocks
go test ./...
go run ./cmd/app
```

Игрок указывает логин, почту и пароль на `/register`, подтверждает почту
по ссылке из письма и входит на `/login`. Логин отображается в рейтинге.
Вход только по нику закрыт: `POST /api/v1/players/join` возвращает `410`.
Параметры Resend и SMTP перечислены в `.env.example`.

## Сервер

[scripts/server-bootstrap.sh](scripts/server-bootstrap.sh) - это скрипт
первичной подготовки Ubuntu/Debian сервера. Он ставит Docker, Docker Compose и
Git, создает пользователя приложения, каталог, `.env` и базовые правила
межсетевого экрана.

Скрипт нужен один раз перед первым запуском сервера и не заменяет
процедуру публикации и развертывания приложения.

Минимальный первый запуск после заполнения `.env`:

```bash
sudo bash scripts/server-bootstrap.sh
cd /opt/task-per-minute/deployment/docker
docker compose --env-file ../../.env up -d --build --remove-orphans
```

Основной серверный Compose собирает backend и frontend из исходников.
Дополнительный файл `deployment/docker/docker-compose.ci.yml` позволяет
запускать тот же стек с заранее собранными образами.

## Источники контракта

- [OpenAPI](backend/api/openapi.yml) - актуальный REST-контракт.
- [Конфигурации Docker Compose](deployment/docker/) - настройки окружений.

## Команда разработки

- [CaXaRo4iK](https://github.com/CaXaRo4iK) - развертывание, инфраструктура и задачи
- [FANATBEBRbl](https://github.com/FANATBEBRbl) - интерфейс
- [TakuyaYagam1](https://github.com/TakuyaYagam1) - серверная часть

## Социальные ссылки

- RedShift Telegram: [@redshift_ctf](https://t.me/redshift_ctf)
- Чат RedShift: [@redshift_ctf_chat](https://t.me/redshift_ctf_chat)

## Лицензия

Copyright (c) 2025 Task Per Minute contributors.

Проект распространяется по [GNU General Public License, версия 3](LICENSE)
(`GPL-3.0-only`). Вы можете распространять и изменять программу на условиях
этой лицензии. Программа предоставляется без каких-либо гарантий, включая
гарантии товарной пригодности и пригодности для конкретной цели.

Сторонние компоненты сохраняют собственные лицензии и уведомления об авторских правах.
