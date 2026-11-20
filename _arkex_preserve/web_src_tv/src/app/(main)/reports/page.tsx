"use client";

import { useState } from "react";
import { useWorkspace } from "@/components/workspace";
import { api } from "@/lib/api";

export default function ReportsPage() {
  const { engagements, selected } = useWorkspace();
  const engagement = engagements.find(({ id }) => id === selected);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<{ id: string; message: string }>();

  async function downloadReport() {
    if (!engagement) return;
    setBusy(true);
    setStatus(undefined);
    try {
      const findings = await api.listFindings(engagement.id);
      const report = { engagement, exported_at: new Date().toISOString(), findings };
      const url = URL.createObjectURL(new Blob([JSON.stringify(report, null, 2)], { type: "application/json" }));
      const link = document.createElement("a");
      link.href = url;
      link.download = `arkex-report-${engagement.id}.json`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setStatus({ id: engagement.id, message: `Report prepared with ${findings.length} ${findings.length === 1 ? "finding" : "findings"}.` });
    } catch {
      setStatus({ id: engagement.id, message: "Could not prepare the report." });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="page-container py-8">
      <div className="workspace-heading"><div><h1>Reports</h1><p>Export a snapshot of the selected engagement and its latest 100 findings.</p></div></div>
      <section className="card">
        <h2 className="mb-3">Engagement report</h2>
        {engagement ? (
          <>
            <p className="mb-4">{engagement.title}</p>
            <button className="btn-primary" type="button" onClick={downloadReport} disabled={busy}>{busy ? "Exporting…" : "Download JSON report"}</button>
            {status?.id === engagement.id && <p role="status" className="mt-3">{status.message}</p>}
          </>
        ) : <p className="workspace-muted">Select an engagement in the header to export its report.</p>}
      </section>
    </div>
  );
}
