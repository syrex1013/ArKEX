"use client";

import { useEffect, useState } from "react";
import { api, type StorageBreakdown } from "@/lib/api";
import { CornerFrame } from "@/components/corner-frame";

export default function SettingsPage() {
  const [storage, setStorage] = useState<StorageBreakdown | null>(null);
  const [tab, setTab] = useState<"storage" | "llm" | "system">("storage");

  useEffect(() => {
    api.getStorage().then(setStorage).catch(console.error);
  }, []);

  return (
    <div className="page-container" style={{ paddingTop: 32, paddingBottom: 64 }}>
      <h1 style={{ fontSize: "var(--text-h1)", marginBottom: 32 }}>System & Settings</h1>

      {/* Tabs */}
      <div
        style={{
          display: "flex",
          borderBottom: "1px solid var(--tv-border)",
          marginBottom: 32,
          gap: 0,
        }}
      >
        {(["storage", "llm", "system"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            style={{
              height: 40,
              padding: "0 20px",
              background: "none",
              border: "none",
              borderBottom: tab === t ? "2px solid var(--tv-primary)" : "2px solid transparent",
              cursor: "pointer",
              fontSize: "var(--text-body-sm)",
              fontWeight: tab === t ? 600 : 400,
              color: tab === t ? "var(--tv-secondary)" : "var(--tv-muted)",
              transition: "color 0.18s",
              marginBottom: -1,
              textTransform: "capitalize",
            }}
          >
            {t === "llm" ? "LLM Pool" : t}
          </button>
        ))}
      </div>

      {tab === "storage" && storage && (
        <div>
          <h2 style={{ fontSize: "var(--text-h2)", marginBottom: 20 }}>Storage Governor</h2>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))", gap: 16, marginBottom: 32 }}>
            <StorageTile label="PostgreSQL" bytes={storage.pg_bytes} />
            <StorageTile label="Blob Store (CAS)" bytes={storage.blob_bytes} />
            <StorageTile label="Traffic FTS" bytes={storage.traffic_fts_bytes} />
            <StorageTile label="Scratch" bytes={storage.scratch_bytes} />
          </div>
          <CornerFrame className="card">
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
              <div>
                <div style={{ fontSize: "var(--text-label-sm)", color: "var(--tv-muted)", textTransform: "uppercase", letterSpacing: "0.05em", marginBottom: 4 }}>
                  Total Footprint
                </div>
                <div style={{ fontSize: "var(--text-metric-md)", fontWeight: 500, fontVariantNumeric: "tabular-nums" }}>
                  {storage.total_gb.toFixed(3)} GB
                </div>
              </div>
              <div style={{ textAlign: "right" }}>
                <div style={{ fontSize: "var(--text-label-sm)", color: "var(--tv-muted)", textTransform: "uppercase", letterSpacing: "0.05em", marginBottom: 4 }}>
                  Zstd Compression
                </div>
                <div style={{ fontSize: "var(--text-metric-md)", fontWeight: 500, color: "var(--tv-verified)", fontVariantNumeric: "tabular-nums" }}>
                  {storage.compression_ratio.toFixed(1)}×
                </div>
              </div>
            </div>
            {/* Compression bar */}
            <div style={{ marginTop: 16 }}>
              <div style={{ height: 4, background: "var(--tv-surface-subtle)", borderRadius: 2, overflow: "hidden" }}>
                <div style={{
                  height: "100%",
                  width: `${Math.min(100, (1 - 1 / storage.compression_ratio) * 100)}%`,
                  background: "var(--tv-verified)",
                  transition: "width 0.6s var(--ease-tactical)",
                }} />
              </div>
              <div style={{ fontSize: "var(--text-label-sm)", color: "var(--tv-muted)", marginTop: 4 }}>
                {((1 - 1 / storage.compression_ratio) * 100).toFixed(0)}% space saved
              </div>
            </div>
          </CornerFrame>
        </div>
      )}

      {tab === "llm" && (
        <div>
          <h2 style={{ fontSize: "var(--text-h2)", marginBottom: 20 }}>LLM Pool Health</h2>
          <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            {[
              { tier: 1, name: "Claude Haiku 5.5", label: "Tier 1 — Fast/Cheap", healthy: true },
              { tier: 2, name: "Claude Sonnet 5.5", label: "Tier 2 — Frontier Reasoning", healthy: true },
              { tier: 3, name: "Ollama (local)", label: "Tier 3 — Uncensored Failover", healthy: false },
            ].map((p) => (
              <div key={p.tier} className="card" style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                <div>
                  <div style={{ fontWeight: 500, marginBottom: 2 }}>{p.name}</div>
                  <div style={{ fontSize: "var(--text-label-sm)", color: "var(--tv-muted)" }}>{p.label}</div>
                </div>
                <span className={`badge ${p.healthy ? "badge-verified" : "badge-unverified"}`}>
                  {p.healthy ? "healthy" : "offline"}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {tab === "system" && (
        <div>
          <h2 style={{ fontSize: "var(--text-h2)", marginBottom: 20 }}>System Configuration</h2>
          <div className="card">
            <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
              <ConfigRow label="ARKEX_MAX_DISK_GB" value="20" />
              <ConfigRow label="ARKEX_DATABASE_URL" value="postgresql://***" />
              <ConfigRow label="ARKEX_SCRATCH_DIR" value="/tmp/arkex-scratch" />
              <ConfigRow label="ARKEX_OAST_DOMAIN" value="oast.threatvector.tech" />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function StorageTile({ label, bytes }: { label: string; bytes: number }) {
  const gb = bytes / 1e9;
  const mb = bytes / 1e6;
  const display = gb >= 0.1 ? `${gb.toFixed(2)} GB` : `${mb.toFixed(0)} MB`;
  return (
    <div className="card">
      <div style={{ fontSize: "var(--text-label-sm)", color: "var(--tv-muted)", textTransform: "uppercase", letterSpacing: "0.05em", marginBottom: 8 }}>
        {label}
      </div>
      <div style={{ fontSize: "var(--text-h2)", fontWeight: 500, fontVariantNumeric: "tabular-nums" }}>
        {display}
      </div>
    </div>
  );
}

function ConfigRow({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
      <span style={{ fontFamily: "var(--font-mono)", fontSize: "var(--text-body-sm)", color: "var(--tv-secondary)" }}>
        {label}
      </span>
      <span style={{ fontFamily: "var(--font-mono)", fontSize: "var(--text-body-sm)", color: "var(--tv-muted)" }}>
        {value}
      </span>
    </div>
  );
}
