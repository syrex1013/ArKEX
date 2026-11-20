"use client";

import { useState } from "react";

export default function LabPage() {
  const [activeTab, setActiveTab] = useState<"cpg" | "fuzzer" | "desync">("cpg");

  return (
    <div className="page-container" style={{ paddingTop: 32, paddingBottom: 64 }}>
      <h1 style={{ fontSize: "var(--text-h1)", marginBottom: 32 }}>0-Day & Protocol Lab</h1>

      <div style={{ display: "flex", borderBottom: "1px solid var(--tv-border)", marginBottom: 32 }}>
        {(["cpg", "fuzzer", "desync"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setActiveTab(t)}
            style={{
              height: 40,
              padding: "0 20px",
              background: "none",
              border: "none",
              borderBottom: activeTab === t ? "2px solid var(--tv-primary)" : "2px solid transparent",
              cursor: "pointer",
              fontSize: "var(--text-body-sm)",
              fontWeight: activeTab === t ? 600 : 400,
              color: activeTab === t ? "var(--tv-secondary)" : "var(--tv-muted)",
              marginBottom: -1,
              textTransform: "uppercase",
              letterSpacing: "0.05em",
            }}
          >
            {t === "cpg" ? "CPG / Taint" : t === "fuzzer" ? "AFL++ / Fuzzer" : "H2 Desync"}
          </button>
        ))}
      </div>

      {activeTab === "cpg" && (
        <div>
          <h2 style={{ fontSize: "var(--text-h2)", marginBottom: 16 }}>Code Property Graph — Taint Paths</h2>
          <div className="card" style={{ padding: 48, textAlign: "center", color: "var(--tv-muted)" }}>
            Index a source repository to see sink-to-source taint paths here.
          </div>
        </div>
      )}

      {activeTab === "fuzzer" && (
        <div>
          <h2 style={{ fontSize: "var(--text-h2)", marginBottom: 16 }}>Hybrid Fuzzer Orchestrator</h2>
          <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
            <div className="card">
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
                <div>
                  <div style={{ fontWeight: 500 }}>AFL++ Container</div>
                  <div style={{ fontSize: "var(--text-body-sm)", color: "var(--tv-muted)" }}>No active jobs</div>
                </div>
                <button className="btn-primary">Launch Fuzzer</button>
              </div>
              <div style={{ height: 4, background: "var(--tv-surface-subtle)", borderRadius: 2 }}>
                <div style={{ height: "100%", width: "0%", background: "var(--tv-primary)", borderRadius: 2 }} />
              </div>
            </div>
          </div>
        </div>
      )}

      {activeTab === "desync" && (
        <div>
          <h2 style={{ fontSize: "var(--text-h2)", marginBottom: 16 }}>HTTP/2 Desync & Single-Packet Attack</h2>
          <div className="card">
            <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
              <div>
                <label style={{ display: "block", fontSize: "var(--text-label-sm)", fontWeight: 600, marginBottom: 6, color: "var(--tv-muted)" }}>
                  TARGET URL
                </label>
                <input type="url" placeholder="https://target.example.com" />
              </div>
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                {["CL.TE", "TE.CL", "H2.CL", "H2.TE", "H2.SP"].map((kind) => (
                  <label key={kind} style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer" }}>
                    <input type="checkbox" defaultChecked style={{ width: 16, height: 16 }} />
                    <span style={{ fontFamily: "var(--font-mono)", fontSize: 13 }}>{kind}</span>
                  </label>
                ))}
              </div>
              <button className="btn-primary" style={{ width: 160 }}>Run Probes</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
