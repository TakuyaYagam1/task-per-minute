import { ArenaPublicTournamentPage } from "../../../../lib/pages/arena/exports";

type PublicTournamentPageProps = Readonly<{
  params: Promise<{ publicId: string }>;
}>;

export default async function PublicTournamentPage({
  params,
}: PublicTournamentPageProps) {
  const { publicId } = await params;
  return <ArenaPublicTournamentPage publicId={publicId} />;
}
