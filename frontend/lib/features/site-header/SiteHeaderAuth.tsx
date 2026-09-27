"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";

type SiteHeaderAuth = Readonly<{
  onLogout?: () => void;
  logoutPending: boolean;
}>;

type RegisteredAuth = SiteHeaderAuth & Readonly<{ id: number }>;

type SiteHeaderAuthContextValue = Readonly<{
  auth: RegisteredAuth | null;
  registerAuth: (auth: SiteHeaderAuth) => () => void;
}>;

const SiteHeaderAuthContext = createContext<SiteHeaderAuthContextValue | null>(null);

export function SiteHeaderAuthProvider({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const nextRegistrationId = useRef(0);
  const [auth, setAuth] = useState<RegisteredAuth | null>(null);

  const registerAuth = useCallback((nextAuth: SiteHeaderAuth) => {
    const id = nextRegistrationId.current + 1;
    nextRegistrationId.current = id;
    setAuth({ ...nextAuth, id });

    return () => {
      setAuth((current) => (current?.id === id ? null : current));
    };
  }, []);

  return (
    <SiteHeaderAuthContext.Provider value={{ auth, registerAuth }}>
      {children}
    </SiteHeaderAuthContext.Provider>
  );
}

export function useSiteHeaderAuth(
  onLogout: (() => void) | undefined,
  logoutPending: boolean,
): void {
  const context = useContext(SiteHeaderAuthContext);
  const registerAuth = context?.registerAuth;
  const onLogoutRef = useRef(onLogout);
  onLogoutRef.current = onLogout;
  const stableOnLogout = useCallback(() => {
    onLogoutRef.current?.();
  }, []);
  const hasLogout = Boolean(onLogout);

  useEffect(() => {
    if (!registerAuth) {
      return undefined;
    }
    return registerAuth({
      onLogout: hasLogout ? stableOnLogout : undefined,
      logoutPending,
    });
  }, [hasLogout, logoutPending, registerAuth, stableOnLogout]);
}

export function useRegisteredSiteHeaderAuth(): SiteHeaderAuth | null {
  return useContext(SiteHeaderAuthContext)?.auth ?? null;
}
