import Link from "next/link";

import { PlayerRegistrationForm } from "../../features/player-auth";
import { PlayerAuthPanel } from "../../widgets/player-auth";

export function PlayerRegistrationPage() {
  return (
    <PlayerAuthPanel
      title="Создать аккаунт"
      description="Укажите email и придумайте уникальный логин. Он будет виден в рейтинге."
      footer={<>Уже зарегистрированы? <Link href="/login">Войти</Link></>}
    >
      <PlayerRegistrationForm />
    </PlayerAuthPanel>
  );
}
