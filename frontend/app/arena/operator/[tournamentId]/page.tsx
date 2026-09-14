import { ArenaRolePage } from "../../../../lib/pages/arena/exports";

type OperatorArenaPageProps = Readonly<{
  params: Promise<{ tournamentId: string }>;
}>;

export default async function OperatorArenaPage({
  params,
}: OperatorArenaPageProps) {
  const { tournamentId } = await params;
  return <ArenaRolePage role="operator" tournamentId={tournamentId} />;
}
