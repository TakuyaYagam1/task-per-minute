import type { Metadata } from "next";

import { ErrorPageShell } from "../lib/widgets/error-page";

export const metadata: Metadata = {
  title: "404 — Task Per Minute",
};

export default function NotFound() {
  return <ErrorPageShell statusCode={404} />;
}
