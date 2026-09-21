---
version: 1
slug: "frontend-lib-pages-arena-arenarolepage-tsx"
primary_target: "frontend/lib/pages/arena/ArenaRolePage.tsx"
related_targets: ["frontend/lib/widgets/tournament-broadcast"]
---

Scope: public spectator route in Arena. Mode: Operate.

## Direction contract

THESIS: Публичная страница работает как матч-центр, где текущее состояние сервера и выбранная встреча важнее общей декоративной сводки. Она отказывается от набора равнозначных карточек и статического таймера.

OWN-WORLD: Наследуется существующая Arena: графитовые поверхности, синий акцент, спокойные границы, компактные радиусы и полноценная светлая тема. Акцент обозначает текущий выбор, связь и активное состояние, а не украшает фон.

STORY: Наблюдатель открывает турнир без входа, сразу понимает этап и состояние связи, выбирает серверный матч, читает счет и статус, затем переходит к таблице или сетке. Прямая ссылка сохраняет выбранный матч.

FIRST VIEWPORT: После постоянного контекста Arena идет широкий матч-центр. Слева находится серверное расписание встреч, справа крупный выбранный матч с этапом, раундом, счетом и состоянием. Ниже в той же системе расположены переключаемые таблица и сетка.

FORM: Локальное расширение установленной Operate-поверхности без concept seed. Сигнатурное взаимодействие - выбор матча одновременно обновляет центральную сводку и query-параметр URL без перезагрузки.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
