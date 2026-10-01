import { Suspense } from "react";

import { SettingsPage } from "@/pages/settings";

export default function SettingsRoute() {
  return (
    <Suspense fallback={null}>
      <SettingsPage />
    </Suspense>
  );
}
