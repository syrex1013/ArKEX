"use client";

import * as React from "react";
import { useState } from "react";

import { toast } from "sonner";

import { CornerFrame } from "@/components/threatvector/corner-frame";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";

type Sink = {
  FilePath?: string;
  file_path?: string;
  LineNo?: number;
  line_no?: number;
  Kind?: string;
  sink_kind?: string;
  SourceLine?: string;
  source_line?: string;
  Confidence?: number;
  confidence?: number;
};

export default function CpgPage() {
  const [repoPath, setRepoPath] = useState("");
  const [language, setLanguage] = useState("auto");
  const [busy, setBusy] = useState(false);
  const [sinks, setSinks] = useState<Sink[]>([]);

  async function run() {
    setBusy(true);
    try {
      const r = await api.labCPGIndex(repoPath, "lab", language);
      setSinks(r.sinks ?? []);
      toast.success(`Indexed ${r.count ?? 0} sinks`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "CPG index failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-[1440px] flex-col gap-6 p-4 md:p-6">
      <CornerFrame className="p-5">
        <div className="border-b-2 border-primary pb-3">
          <h1 className="text-[1.25rem] font-medium tracking-tight">CPG / Taint</h1>
          <p className="text-muted-foreground mt-1 text-sm">Code Property Graph sink indexer</p>
        </div>
      </CornerFrame>

      <Card>
        <CardHeader>
          <CardTitle>Code Property Graph</CardTitle>
          <CardDescription>Sink indexer over harvested source under data/lab (or abs with ARKEX_LAB_ALLOW_ABS=1).</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5 sm:col-span-2">
              <Label htmlFor="cpg-path">Source path</Label>
              <Input id="cpg-path" value={repoPath} onChange={(e) => setRepoPath(e.target.value)} placeholder="relative to data/lab or absolute" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="cpg-lang">Language</Label>
              <Input id="cpg-lang" value={language} onChange={(e) => setLanguage(e.target.value)} placeholder="auto|go|js|py|…" />
            </div>
          </div>
          <Button onClick={run} disabled={busy || !repoPath.trim()}>
            {busy ? "Indexing…" : "Index sinks"}
          </Button>
          <ul className="max-h-96 space-y-2 overflow-auto text-sm">
            {sinks.map((s, i) => (
              <li key={i} className="rounded-md border border-border px-3 py-2 font-mono text-xs">
                <div>
                  {(s.Kind ?? s.sink_kind) as string} · {(s.FilePath ?? s.file_path) as string}:{(s.LineNo ?? s.line_no) as number}
                </div>
                <div className="text-muted-foreground mt-1">{(s.SourceLine ?? s.source_line) as string}</div>
              </li>
            ))}
          </ul>
        </CardContent>
      </Card>
    </div>
  );
}
