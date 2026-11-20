"use client";

import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { api, type Engagement } from "@/lib/api";

type Workspace = {
  engagements: Engagement[];
  selected: string;
  select: (id: string) => void;
  loading: boolean;
  error: boolean;
};

const WorkspaceContext = createContext<Workspace>({
  engagements: [], selected: "", select: () => {}, loading: true, error: false,
});

export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const [engagements, setEngagements] = useState<Engagement[]>([]);
  const [selected, select] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  useEffect(() => {
    api.listEngagements().then(setEngagements).catch(() => setError(true)).finally(() => setLoading(false));
  }, []);

  return (
    <WorkspaceContext.Provider value={{ engagements, selected, select, loading, error }}>
      {children}
    </WorkspaceContext.Provider>
  );
}

export function useWorkspace() {
  return useContext(WorkspaceContext);
}
