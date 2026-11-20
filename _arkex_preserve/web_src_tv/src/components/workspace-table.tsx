"use client";

import { useEffect, useState, type ReactNode } from "react";
import { useWorkspace } from "@/components/workspace";

export function WorkspaceTable<Row extends { id: string }>({
  title, description, load, columns,
}: {
  title: string;
  description: string;
  load: (id: string) => Promise<Row[]>;
  columns: { label: string; value: (row: Row) => ReactNode }[];
}) {
  const { selected } = useWorkspace();
  return (
    <div className="page-container py-8">
      <div className="workspace-heading"><div><h1>{title}</h1><p>{description}</p></div></div>
      {selected ? <Records key={selected} engagement={selected} load={load} columns={columns} /> : (
        <p className="card workspace-muted">Select an engagement in the header.</p>
      )}
    </div>
  );
}

function Records<Row extends { id: string }>({ engagement, load, columns }: {
  engagement: string;
  load: (id: string) => Promise<Row[]>;
  columns: { label: string; value: (row: Row) => ReactNode }[];
}) {
  const [records, setRecords] = useState<Row[]>();
  const [error, setError] = useState(false);
  const [query, setQuery] = useState("");

  useEffect(() => {
    let active = true;
    load(engagement).then((items) => { if (active) setRecords(items); }).catch(() => { if (active) setError(true); });
    return () => { active = false; };
  }, [engagement, load]);

  if (!records?.length) {
    return <p role="status" className="card workspace-muted">{error ? "Could not load records." : records ? "No records yet." : "Loading records…"}</p>;
  }

  const normalized = query.trim().toLowerCase();
  const visible = records.filter((row) => columns.some(({ value }) => String(value(row) ?? "").toLowerCase().includes(normalized)));
  return (
    <div className="card">
      <input type="search" className="mb-4" value={query} onChange={(event) => setQuery(event.target.value)} aria-label="Filter records" placeholder="Filter records…" />
      {visible.length ? (
        <div className="overflow-x-auto">
          <table>
            <thead><tr>{columns.map(({ label }) => <th key={label} scope="col">{label}</th>)}</tr></thead>
            <tbody>{visible.map((row) => (
              <tr key={row.id}>{columns.map(({ label, value }) => <td key={label}>{value(row)}</td>)}</tr>
            ))}</tbody>
          </table>
        </div>
      ) : <p className="workspace-muted">No matching records.</p>}
    </div>
  );
}
