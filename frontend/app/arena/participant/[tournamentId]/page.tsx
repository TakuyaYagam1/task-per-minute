import { ArenaRolePage } from "../../../../lib/pages/arena/exports";

type ParticipantArenaPageProps = Readonly<{
  params: Promise<{ tournamentId: string }>;
}>;

export default async function ParticipantArenaPage({
  params,
}: ParticipantArenaPageProps) {
  const { tournamentId } = await params;
  return <ArenaRolePage role="participant" tournamentId={tournamentId} />;
}
