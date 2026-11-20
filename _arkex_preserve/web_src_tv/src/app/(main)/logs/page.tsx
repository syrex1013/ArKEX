"use client";

import { WorkspaceTable } from "@/components/workspace-table";
import { api } from "@/lib/api";

export default function LogsPage() {
  return <WorkspaceTable title="Logs" description="Governance decisions recorded for the selected engagement." load={api.listDecisions} columns={[
    { label: "Action", value: (row) => row.action_kind },
    { label: "Decision", value: (row) => row.verdict },
    { label: "Rule", value: (row) => row.rule_code },
    { label: "Recorded", value: (row) => new Date(row.created_at).toLocaleString() },
  ]} />;
}
