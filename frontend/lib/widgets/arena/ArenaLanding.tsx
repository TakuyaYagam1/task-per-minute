import Link from "next/link";

import { Panel } from "../../shared/ui";

import {
  buildArenaRolePath,
  isSafeTournamentId,
} from "./ArenaShell";
import styles from "./arena.module.css";
import type { ArenaRole } from "./types";

type ArenaLandingProps = Readonly<{
  tournamentId: string;
  onTournamentIdChange: (value: string) => void;
}>;

const ROLE_CARDS: ReadonlyArray<Readonly<{
  role: ArenaRole;
  title: string;
  description: string;
}>> = [
  {
    role: "participant",
    title: "Участник",
    description: "Личное лобби и статус вашей серии.",
  },
  {
    role: "operator",
    title: "Оператор",
    description: "Контрольный снимок и состояние турнира.",
  },
  {
    role: "spectator",
    title: "Наблюдатель",
    description: "Публичный просмотр без входа.",
  },
];

export const ArenaLanding = ({
  onTournamentIdChange,
  tournamentId,
}: ArenaLandingProps) => {
  const normalizedId = tournamentId.trim();
  const canOpenRole = isSafeTournamentId(normalizedId);

  return (
    <div className={styles.landing}>
      <section className={styles.hero} aria-labelledby="arena-landing-title">
        <h1 className={styles.heroTitle} id="arena-landing-title">Arena</h1>
        <p className={styles.heroLead}>
          Единая точка входа в турнир: выберите контекст и продолжите с нужной ролью.
        </p>
        <p className={styles.heroNote}>
          Идентификатор турнира остается в адресе, поэтому прямую ссылку можно безопасно обновить.
        </p>
      </section>

      <div className={styles.landingPanel}>
        <Panel
          title="Открыть турнир"
          description="Укажите публичный идентификатор турнира."
        >
          <div
            className={styles.form}
            aria-label="Выбор турнира"
          >
            <label className={styles.fieldLabel} htmlFor="arena-tournament-id-input">
              Идентификатор турнира
            </label>
            <input
              className={styles.input}
              id="arena-tournament-id-input"
              name="tournamentId"
              type="text"
              value={tournamentId}
              onChange={(event) => onTournamentIdChange(event.target.value)}
              placeholder="Например, 00000000-0000-4000-8000-000000000001"
              autoComplete="off"
              spellCheck={false}
              maxLength={128}
              required
            />
            <p className={styles.fieldHint}>
              Ссылка откроется после выбора роли.
            </p>
          </div>
        </Panel>

        {canOpenRole && (
          <section className={styles.roleChoices} aria-labelledby="arena-role-choices-title">
            <h2 className={styles.roleChoicesTitle} id="arena-role-choices-title">
              Выберите роль
            </h2>
            <div className={styles.roleGrid}>
              {ROLE_CARDS.map((card) => (
                <Link
                  key={card.role}
                  className={styles.roleCard}
                  href={buildArenaRolePath(card.role, normalizedId)}
                >
                  <span className={styles.roleCardTitle}>{card.title}</span>
                  <span className={styles.roleCardDescription}>{card.description}</span>
                </Link>
              ))}
            </div>
          </section>
        )}
      </div>
    </div>
  );
};

ArenaLanding.displayName = "ArenaLanding";
