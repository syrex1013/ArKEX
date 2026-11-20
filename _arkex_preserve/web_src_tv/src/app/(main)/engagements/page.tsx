"use client";

import Link from "next/link";
import { useWorkspace } from "@/components/workspace";

export default function EngagementsPage() {
  const { engagements, selected, select, loading, error } = useWorkspace();

  return (
    <div className="page-container py-8">
      <div className="workspace-heading"><div><h1>Engagements</h1><p>Choose an engagement to browse its tasks, findings, reports, and notes.</p></div></div>
      <div className="card">
        {!engagements.length ? <p className="workspace-muted">{loading ? "Loading engagements…" : error ? "Could not load engagements." : "No engagements yet."}</p> : (
          <ul className="workspace-list">
            {engagements.map(({ id, title, status, created_at }) => (
              <li key={id}>
                <div><strong>{title}</strong><p className="workspace-muted">{status} · {new Date(created_at).toLocaleDateString()}</p></div>
                <button className="btn-secondary" type="button" onClick={() => select(id)} aria-pressed={selected === id}>
                  {selected === id ? "Selected" : "Select"}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      {selected && (
        <nav aria-label="Engagement actions" className="flex flex-wrap gap-2 mt-4">
          <Link className="btn-secondary" href="/tasks/">Tasks</Link>
          <Link className="btn-secondary" href="/findings/">Findings</Link>
          <Link className="btn-secondary" href="/reports/">Reports</Link>
          <Link className="btn-secondary" href="/discussion/">Discussion</Link>
        </nav>
      )}
    </div>
  );
}
