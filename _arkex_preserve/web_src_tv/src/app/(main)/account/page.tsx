"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api";

export default function AccountPage() {
  const [version, setVersion] = useState("Checking…");
  const [health, setHealth] = useState("Checking…");

  useEffect(() => {
    api.version().then(({ version }) => setVersion(version)).catch(() => setVersion("Unavailable"));
    api.health().then(() => setHealth("Healthy")).catch(() => setHealth("Unavailable"));
  }, []);

  return (
    <div className="page-container py-8">
      <div className="workspace-heading"><div><h1>Account</h1><p>This ArKex installation uses a local session.</p></div></div>
      <section className="card">
        <h2 className="mb-3">Local session</h2>
        <dl className="workspace-list">
          <div><dt>Access</dt><dd>Local browser session</dd></div>
          <div><dt>ArKex version</dt><dd>{version}</dd></div>
          <div><dt>Backend status</dt><dd>{health}</dd></div>
        </dl>
      </section>
    </div>
  );
}
