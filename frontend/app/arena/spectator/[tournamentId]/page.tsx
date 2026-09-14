import { ArenaRolePage } from "../../../../lib/pages/arena/exports";

type SpectatorArenaPageProps = Readonly<{
  params: Promise<{ tournamentId: string }>;
}>;

export default async function SpectatorArenaPage({
  params,
}: SpectatorArenaPageProps) {
  const { tournamentId } = await params;
  return <ArenaRolePage role="spectator" tournamentId={tournamentId} />;
}
