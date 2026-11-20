"use client";

import { WorkspaceTable } from "@/components/workspace-table";
import { api } from "@/lib/api";

export default function AssetsPage() {
  return <WorkspaceTable title="Assets" description="Recently seen assets for the selected engagement’s organization." load={api.listAssets} columns={[
    { label: "Asset", value: (row) => row.value },
    { label: "Type", value: (row) => row.kind.replaceAll("_", " ") },
    { label: "Last seen", value: (row) => new Date(row.last_seen).toLocaleString() },
  ]} />;
}
