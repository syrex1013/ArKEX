"use client";

import { useEffect, useState } from "react";
import { motion } from "framer-motion";
import { CornerFrame, LiveDot } from "@/components/corner-frame";
import { api, type Engagement, type StorageBreakdown } from "@/lib/api";

export default function Dashboard() {
  const [engagements, setEngagements] = useState<Engagement[]>([]);
  const [storage, setStorage] = useState<StorageBreakdown | null>(null);
  const [counts, setCounts] = useState({ active: 0, findings: 0, oast: 0 });

  useEffect(() => {
    api.listEngagements().then(setEngagements).catch(console.error);
    api.getStorage().then(setStorage).catch(console.error);
  }, []);

  useEffect(() => {
    const active = engagements.filter((e) => e.status === "running").length;
    setCounts((c) => ({ ...c, active }));
  }, [engagements]);

  return (
    <main className="page-container" style={{ paddingTop: 32, paddingBottom: 64 }}>
      {/* Header */}
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 32 }}>
        <div>
          <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
            <span style={{ color: "var(--tv-primary)", fontWeight: 700, fontSize: 20, fontFamily: "var(--font-mono)" }}>
              [ARKEX]
            </span>
            <span style={{ fontSize: "var(--text-label-sm)", color: "var(--tv-muted)", fontFamily: "var(--font-mono)" }}>
              v2.0.0
            </span>
          </div>
          <h1 style={{ fontSize: "var(--text-h1)", marginTop: 4 }}>Command Dashboard</h1>
        </div>
        <a href="/engagements/new" className="btn-primary">
          Launch Engagement
        </a>
      </div>

      {/* KPI Strip */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))",
          gap: 16,
          marginBottom: 48,
        }}
      >
        <KPITile
          label="Active Engagements"
          value={counts.active}
          active={counts.active > 0}
        />
        <KPITile label="Verified Findings" value={counts.findings} />
        <KPITile label="OAST Hits" value={counts.oast} />
        <KPITile
          label="Disk Footprint"
          value={storage ? `${storage.total_gb.toFixed(2)} GB` : "—"}
          suffix={storage ? `${storage.compression_ratio.toFixed(1)}× saved` : ""}
        />
      </div>

      {/* Engagements Table */}
      <div style={{ marginBottom: 48 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 16 }}>
          <h2 style={{ fontSize: "var(--text-h2)" }}>Engagements</h2>
          <hr className="section-rule" style={{ flex: 1 }} />
        </div>

        {engagements.length === 0 ? (
          <div className="card" style={{ textAlign: "center", padding: 48, color: "var(--tv-muted)" }}>
            No engagements yet. Launch one to begin.
          </div>
        ) : (
          <div className="card" style={{ padding: 0 }}>
            <table>
              <thead>
                <tr>
                  <th>Title</th>
                  <th>Status</th>
                  <th>Created</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {engagements.map((e) => (
                  <motion.tr
                    key={e.id}
                    initial={{ opacity: 0, y: 6 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ duration: 0.22, ease: [0.16, 1, 0.3, 1] }}
                  >
                    <td style={{ fontWeight: 500 }}>{e.title}</td>
                    <td>
                      <StatusBadge status={e.status} />
                    </td>
                    <td style={{ color: "var(--tv-muted)" }}>
                      {new Date(e.created_at).toLocaleDateString()}
                    </td>
                    <td>
                      <a
                        href={`/tasks/${e.id}`}
                        style={{ color: "var(--tv-primary)", fontSize: "var(--text-body-sm)", textDecoration: "none" }}
                      >
                        View →
                      </a>
                    </td>
                  </motion.tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </main>
  );
}

function KPITile({
  label,
  value,
  active,
  suffix,
}: {
  label: string;
  value: string | number;
  active?: boolean;
  suffix?: string;
}) {
  return (
    <CornerFrame className="card" animate>
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 8 }}>
        <span
          style={{
            fontSize: "var(--text-label-sm)",
            fontWeight: 600,
            color: "var(--tv-muted)",
            textTransform: "uppercase",
            letterSpacing: "0.05em",
          }}
        >
          {label}
        </span>
        {active && <LiveDot active />}
      </div>
      <motion.div
        className="tabular"
        style={{ fontSize: "var(--text-metric-md)", fontWeight: 500 }}
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ duration: 0.32 }}
      >
        {value}
      </motion.div>
      {suffix && (
        <div style={{ fontSize: "var(--text-label-sm)", color: "var(--tv-muted)", marginTop: 4 }}>
          {suffix}
        </div>
      )}
    </CornerFrame>
  );
}

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    running: "badge-critical",
    done: "badge-verified",
    paused: "badge-low",
    pending: "badge-unverified",
    cancelled: "badge-unverified",
  };
  return (
    <span className={`badge ${colors[status] ?? "badge-unverified"}`}>
      {status}
    </span>
  );
}
