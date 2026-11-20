"use client";

import { useState } from "react";

export default function LabPage() {
  const [activeTab, setActiveTab] = useState<"cpg" | "fuzzer" | "desync">("cpg");

  return (
    <div className="page-container" style={{ paddingTop: 32, paddingBottom: 64 }}>
      <h1 style={{ fontSize: "var(--text-h1)", marginBottom: 32 }}>0-Day & Protocol Lab</h1>

      <div style={{ display: "flex", flexWrap: "wrap", borderBottom: "1px solid var(--tv-border)", marginBottom: 32 }}>
        {(["cpg", "fuzzer", "desync"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setActiveTab(t)}
            aria-pressed={activeTab === t}
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

      <div className="card">
        {activeTab === "cpg" && "Source graph results are unavailable in the Web UI."}
        {activeTab === "fuzzer" && "Fuzzer controls are unavailable in the Web UI."}
        {activeTab === "desync" && "Protocol probe controls are unavailable in the Web UI."}
      </div>
    </div>
  );
}
