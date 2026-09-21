---
version: 1
slug: "frontend-lib-features-tournament-operator-actions-tournamentoperatoractions-tsx"
primary_target: "frontend/lib/features/tournament-operator-actions/TournamentOperatorActions.tsx"
related_targets: ["frontend/lib/pages/arena/ArenaRolePage.tsx", "frontend/lib/features/tournament-live"]
---

Scope: authenticated tournament operator recovery controls inside the existing Arena. Mode: Operate.

## Direction contract

THESIS: Оператор сначала видит серверный снимок и причину остановки, затем выбирает только разрешенный сервером путь восстановления. Каждый запуск читается как одна проверяемая команда, а не как локальное продолжение игры.

OWN-WORLD: Наследуется установленная Arena: темный графит и полноценная светлая тема, спокойные границы, компактные радиусы, синий только для фокуса и разрешенного действия. Ошибки и устаревшие снимки остаются заметными, но не превращаются в декоративный шум.

STORY: Оператор видит no-solve или failure, pause reason, категорию и упорядоченную цепочку попыток; для replay доступна только серверная control kind с `replay.available`, для reserve exhaustion - только серверный список кандидатов. После команды экран перечитывает авторитетный снимок, включая конфликт 409.

FIRST VIEWPORT: Existing tournament controls remain first-class. Recovery controls follow as a compact, responsive decision panel with one primary action, exact evidence summary, and inline stale/error feedback.

FORM: Расширение существующей Operate-поверхности без новой визуальной системы. У каждого действия есть явное состояние loading, disabled, success, error и keyboard-visible focus; на мобильном вывод становится одной колонкой без горизонтального скролла.

FINISH: brief is concise by design; the shipping check covers both themes, narrow viewport, keyboard flow, server-only candidates, fresh replacement identities, and no optimistic restart.
