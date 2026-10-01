import Link from "next/link";

import { PlayerRegistrationForm } from "../../features/player-auth";
import { PlayerAuthPanel } from "../../widgets/player-auth";

export function PlayerRegistrationPage() {
  return (
    <PlayerAuthPanel
      title="Создание аккаунта"
      variant="centered"
      footer={<>Уже зарегистрированы? <Link href="/login">Войти</Link></>}
    >
      <PlayerRegistrationForm />
    </PlayerAuthPanel>
  );
}
