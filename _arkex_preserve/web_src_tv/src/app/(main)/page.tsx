"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { useWorkspace } from "@/components/workspace";
import { api, type StorageBreakdown } from "@/lib/api";

export default function Dashboard() {
  const { engagements, loading, error } = useWorkspace();
  const [storage, setStorage] = useState<StorageBreakdown | null>(null);
  const [storageError, setStorageError] = useState(false);

  useEffect(() => {
    api.getStorage().then(setStorage).catch(() => setStorageError(true));
  }, []);

  const metrics = [
    ["Engagements", engagements.length],
    ["Active", engagements.filter(({ status }) => status === "running").length],
    ["Completed", engagements.filter(({ status }) => status === "done").length],
  ];

  return (
    <div className="page-container py-8">
      <div className="workspace-heading">
        <div><h1>Dashboard</h1><p>Engagement activity and local storage.</p></div>
        <Link className="btn-primary" href="/engagements/">View engagements</Link>
      </div>
      <div className="metric-grid">
        {metrics.map(([label, count]) => (
          <div className="card" key={label}><p>{label}</p><strong>{loading || error ? "—" : count}</strong></div>
        ))}
        <div className="card"><p>Disk footprint</p><strong>{storage ? `${storage.total_gb.toFixed(2)} GB` : storageError ? "Unavailable" : "—"}</strong></div>
      </div>
      <section aria-labelledby="recent-engagements" className="card">
        <h2 id="recent-engagements" className="mb-3">Recent engagements</h2>
        {engagements.length ? (
          <ul className="workspace-list">
            {engagements.slice(0, 5).map(({ id, title, status }) => (
              <li key={id}><span>{title}</span><span className="badge badge-unverified">{status}</span></li>
            ))}
          </ul>
        ) : <p className="workspace-muted">{loading ? "Loading engagements…" : error ? "Could not load engagements." : "No engagements yet."}</p>}
      </section>
    </div>
  );
}
