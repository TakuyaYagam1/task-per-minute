"use client";

import { useState } from "react";

import { ArenaLanding, ArenaShell } from "../../widgets/arena";

export const ArenaLandingPage = () => {
  const [tournamentId, setTournamentId] = useState("");

  return (
    <ArenaShell accessStatus="ready">
      <ArenaLanding
        tournamentId={tournamentId}
        onTournamentIdChange={setTournamentId}
      />
    </ArenaShell>
  );
};

ArenaLandingPage.displayName = "ArenaLandingPage";
