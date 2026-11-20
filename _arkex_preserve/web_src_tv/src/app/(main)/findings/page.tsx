"use client";

import { useEffect, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { api, type Finding } from "@/lib/api";
import { CornerFrame } from "@/components/corner-frame";
import { useWorkspace } from "@/components/workspace";

const SEVERITIES = ["critical", "high", "medium", "low", "info"] as const;
const VERIFICATIONS = ["s1_http", "s2_browser", "s3_oast", "s4_sandbox", "unverified"] as const;

export default function FindingsPage() {
  const [findings, setFindings] = useState<Finding[]>([]);
  const [severity, setSeverity] = useState("");
  const [status, setStatus] = useState("");
  const { selected: engId } = useWorkspace();
  const [error, setError] = useState("");

  useEffect(() => {
    setError("");
    setFindings([]);
    if (!engId) return;
    let current = true;
    api.listFindings(engId, { severity: severity || undefined, status: status || undefined })
      .then((items) => { if (current) setFindings(items); })
      .catch(() => { if (current) setError("Could not load findings."); });
    return () => { current = false; };
  }, [engId, severity, status]);

  const counts = SEVERITIES.reduce((acc, s) => {
    acc[s] = findings.filter((f) => f.severity === s).length;
    return acc;
  }, {} as Record<string, number>);

  return (
    <div className="page-container" style={{ paddingTop: 32, paddingBottom: 64 }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 32 }}>
        <h1 style={{ fontSize: "var(--text-h1)" }}>Findings & S1–S4 Receipt Center</h1>
      </div>

      {/* Severity summary */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fit, minmax(110px, 1fr))",
          gap: 12,
          marginBottom: 32,
        }}
      >
        {SEVERITIES.map((s) => (
          <CornerFrame key={s} className="card" style={{ textAlign: "center" }}>
            <div
              style={{ fontSize: "var(--text-metric-md)", fontWeight: 500, fontVariantNumeric: "tabular-nums" }}
            >
              {counts[s] ?? 0}
            </div>
            <div style={{ fontSize: "var(--text-label-sm)", textTransform: "capitalize", color: "var(--tv-muted)" }}>
              {s}
            </div>
          </CornerFrame>
        ))}
      </div>

      {/* Filters */}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 12, marginBottom: 20 }}>
        <select aria-label="Severity" value={severity} onChange={(e) => setSeverity(e.target.value)} disabled={!engId}>
          <option value="">All severities</option>
          {SEVERITIES.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <select aria-label="Status" value={status} onChange={(e) => setStatus(e.target.value)} disabled={!engId}>
          <option value="">All statuses</option>
          {["open", "triaged", "verified", "false_positive", "wont_fix", "fixed"].map((s) => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>
      </div>

      {/* Findings table */}
      <div className="card overflow-x-auto" style={{ padding: 0 }}>
        {findings.length === 0 ? (
          <div style={{ padding: 48, textAlign: "center", color: "var(--tv-muted)" }}>
            {error || (engId ? "No findings match the filters." : "Findings appear here when an engagement is selected.")}
          </div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Title</th>
                <th>Severity</th>
                <th>Verification</th>
                <th>Status</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              <AnimatePresence>
                {findings.map((f) => (
                  <motion.tr
                    key={f.id}
                    initial={{ opacity: 0, y: 6 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ duration: 0.22 }}
                  >
                    <td style={{ fontWeight: 500 }}>{f.title}</td>
                    <td><SeverityBadge severity={f.severity} /></td>
                    <td><VerificationBadge verification={f.verification} /></td>
                    <td style={{ color: "var(--tv-muted)", fontSize: "var(--text-body-sm)" }}>{f.status}</td>
                    <td style={{ color: "var(--tv-muted)", fontSize: "var(--text-body-sm)" }}>
                      {new Date(f.created_at).toLocaleDateString()}
                    </td>
                  </motion.tr>
                ))}
              </AnimatePresence>
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function SeverityBadge({ severity }: { severity: string }) {
  const cls: Record<string, string> = {
    critical: "badge-critical",
    high: "badge-high",
    medium: "badge-medium",
    low: "badge-low",
    info: "badge-info",
  };
  return <span className={`badge ${cls[severity] ?? "badge-info"}`}>{severity}</span>;
}

function VerificationBadge({ verification }: { verification: string }) {
  if (verification === "unverified") return <span className="badge badge-unverified">unverified</span>;
  return <span className="badge badge-verified">{verification}</span>;
}
