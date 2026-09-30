import Link from "next/link";

import { EmailVerificationPanel } from "../../features/player-auth";
import { PlayerAuthPanel } from "../../widgets/player-auth";

export function PlayerEmailVerificationPage() {
  return (
    <PlayerAuthPanel
      title="Подтверждение email"
      description="Подтвердите адрес из письма, чтобы войти в аккаунт."
      footer={<>Уже подтвердили email? <Link href="/login">Перейти ко входу</Link></>}
    >
      <EmailVerificationPanel />
    </PlayerAuthPanel>
  );
}
