export type ArenaRole = "participant" | "operator" | "spectator";

export type ArenaAccessStatus =
  | "loading"
  | "ready"
  | "completed"
  | "unauthorized"
  | "forbidden"
  | "missing"
  | "transport";

export type ArenaMessageTone = "info" | "success" | "warning" | "error" | "loading";

export type ArenaMetric = Readonly<{
  label: string;
  value: string;
}>;

export type ArenaRoleSummary = Readonly<{
  title: string;
  description: string;
  metrics: readonly ArenaMetric[];
  readOnly?: boolean;
}>;

export type ArenaAccessMessage = Readonly<{
  title: string;
  description: string;
  tone: ArenaMessageTone;
  action?: Readonly<{
    href: string;
    label: string;
  }>;
}>;
