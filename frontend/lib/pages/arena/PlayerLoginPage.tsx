import Link from "next/link";

import { PlayerLoginForm } from "../../features/player-auth";
import { PlayerAuthPanel } from "../../widgets/player-auth";

export function PlayerLoginPage() {
  return (
    <PlayerAuthPanel
      title="Вход участника"
      variant="centered"
      footer={
        <>
          Нет аккаунта? <Link href="/register">Создать аккаунт</Link>
        </>
      }
    >
      <PlayerLoginForm />
    </PlayerAuthPanel>
  );
}
