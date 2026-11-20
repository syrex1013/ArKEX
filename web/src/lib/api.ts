const BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body !== undefined ? JSON.stringify(body) : undefined,
    cache: "no-store",
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error ?? res.statusText);
  }
  return res.json();
}

export const api = {
  // Engagements
  listEngagements: () => req<Engagement[]>("GET", "/api/engagements"),
  createEngagement: (data: { company_id: string; title: string; goal: string }) =>
    req<{ id: string }>("POST", "/api/engagements", data),
  getEngagement: (id: string) => req<EngagementStatus>("GET", `/api/engagements/${id}`),
  launchEngagement: (id: string) => req<void>("POST", `/api/engagements/${id}/launch`),
  pauseEngagement: (id: string) => req<void>("POST", `/api/engagements/${id}/pause`),

  // Intents
  listIntents: (engId: string) => req<Intent[]>("GET", `/api/engagements/${engId}/intents`),
  steerWork: (intentId: string, directive: string) =>
    req<void>("POST", `/api/intents/${intentId}/steer`, { directive }),
  killWork: (intentId: string) => req<void>("POST", `/api/intents/${intentId}/kill`),

  // Findings
  listFindings: (engId: string, filters?: { severity?: string; status?: string }) => {
    const p = new URLSearchParams();
    if (filters?.severity) p.set("severity", filters.severity);
    if (filters?.status) p.set("status", filters.status);
    return req<Finding[]>("GET", `/api/engagements/${engId}/findings?${p}`);
  },
  retestFinding: (findingId: string) =>
    req<{ retest_id: string }>("POST", `/api/findings/${findingId}/retest`),

  // Approvals
  listIntercepts: () => req<Intercept[]>("GET", "/api/intercepts"),
  approveIntercept: (id: string) => req<void>("POST", `/api/intercepts/${id}/approve`),
  denyIntercept: (id: string) => req<void>("POST", `/api/intercepts/${id}/deny`),

  // Storage
  getStorage: () => req<StorageBreakdown>("GET", "/api/system/storage"),

  // Companies
  listCompanies: () => req<Company[]>("GET", "/api/companies"),
  createCompany: (data: { name: string; slug: string; scope: string }) =>
    req<{ id: string }>("POST", "/api/companies", data),
};

// Types
export interface Engagement {
  id: string;
  title: string;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface EngagementStatus {
  engagement_id: string;
  status: string;
  started_at: string;
  finished_at: string;
  intents: Intent[];
}

export interface Intent {
  id: string;
  kind: string;
  title: string;
  status: string;
  tokens: number;
  created_at: string;
}

export interface Finding {
  id: string;
  title: string;
  severity: "critical" | "high" | "medium" | "low" | "info";
  status: string;
  verification: string;
  created_at: string;
}

export interface Intercept {
  id: string;
  kind: string;
  payload: string;
  status: string;
  created_at: string;
}

export interface StorageBreakdown {
  pg_bytes: number;
  blob_bytes: number;
  traffic_fts_bytes: number;
  scratch_bytes: number;
  cold_bytes: number;
  compression_ratio: number;
  total_gb: number;
}

export interface Company {
  id: string;
  name: string;
  slug: string;
  created_at: string;
}
