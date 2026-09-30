import Link from "next/link";

import { PlayerLoginForm } from "../../features/player-auth";
import { PlayerAuthPanel } from "../../widgets/player-auth";

export function PlayerLoginPage() {
  return (
    <PlayerAuthPanel
      title="Вход участника"
      description="Войдите по логину или email и паролю."
      footer={
        <>
          Нет аккаунта? <Link href="/register">Создать аккаунт</Link>
          <br />
          Не пришло письмо? <Link href="/verify-email">Запросить подтверждение</Link>
        </>
      }
    >
      <PlayerLoginForm />
    </PlayerAuthPanel>
  );
}
