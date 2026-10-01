import { EmailVerificationPanel } from "../../features/player-auth";
import { PlayerAuthPanel } from "../../widgets/player-auth";

export function PlayerEmailVerificationPage() {
  return (
    <PlayerAuthPanel
      title="Подтверждение email"
      variant="centered"
    >
      <EmailVerificationPanel />
    </PlayerAuthPanel>
  );
}
