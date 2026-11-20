"use client";

import { useState } from "react";

export default function TrafficPage() {
  const [query, setQuery] = useState("");

  return (
    <div className="page-container" style={{ paddingTop: 32, paddingBottom: 64 }}>
      <h1 style={{ fontSize: "var(--text-h1)", marginBottom: 32 }}>MITM Traffic & Auth Vault</h1>

      {/* Search */}
      <div style={{ display: "flex", gap: 12, marginBottom: 24 }}>
        <input
          type="text"
          placeholder="Full-text search traffic (≤64KiB indexed bodies)..."
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          style={{ flex: 1 }}
        />
        <button className="btn-primary" style={{ width: 100 }}>Search</button>
      </div>

      {/* Auth Vault */}
      <div style={{ marginBottom: 32 }}>
        <h2 style={{ fontSize: "var(--text-h2)", marginBottom: 16 }}>Multi-Identity Auth Vault</h2>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))", gap: 12 }}>
          {["Unauth", "User A", "User B", "Admin"].map((label) => (
            <div
              key={label}
              className="card"
              style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}
            >
              <span style={{ fontWeight: 500, fontSize: "var(--text-body-sm)" }}>{label}</span>
              <span className="badge badge-unverified">configure</span>
            </div>
          ))}
        </div>
      </div>

      {/* Traffic table placeholder */}
      <div className="card" style={{ padding: 48, textAlign: "center", color: "var(--tv-muted)" }}>
        Select an engagement to view intercepted traffic.
      </div>
    </div>
  );
}
