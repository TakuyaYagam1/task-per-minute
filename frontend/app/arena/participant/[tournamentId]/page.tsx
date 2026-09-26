import { ArenaRolePage } from "../../../../lib/pages/arena/exports";

type ParticipantArenaPageProps = Readonly<{
  params: Promise<{ tournamentId: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}>;

export default async function ParticipantArenaPage({
  params,
  searchParams,
}: ParticipantArenaPageProps) {
  const { tournamentId } = await params;
  const query = await searchParams;
  const returnPath = typeof query.return === "string" ? query.return : null;
  return <ArenaRolePage role="participant" returnPath={returnPath} tournamentId={tournamentId} />;
}
