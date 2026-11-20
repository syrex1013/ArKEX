"use client";

import { WorkspaceTable } from "@/components/workspace-table";
import { api } from "@/lib/api";

export default function TrafficPage() {
  return <WorkspaceTable title="Traffic" description="Request metadata recorded for the selected engagement." load={api.listTraffic} columns={[
    { label: "Method", value: (row) => row.method },
    { label: "Host", value: (row) => row.host },
    { label: "Status", value: (row) => row.status_code ?? "—" },
    { label: "Recorded", value: (row) => new Date(row.created_at).toLocaleString() },
  ]} />;
}
