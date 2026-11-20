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

type DesyncResult = {
  Kind?: string;
  kind?: string;
  Vulnerable?: boolean;
  vulnerable?: boolean;
  Evidence?: string;
  evidence?: string;
  Error?: string;
  error?: string;
  Latency?: number;
  latency?: number;
};

export default function DesyncPage() {
  const [target, setTarget] = useState("https://example.com/");
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<DesyncResult[]>([]);

  async function run() {
    setBusy(true);
    try {
      const r = await api.labDesync(target);
      setResults(r.results ?? []);
      toast.success("Desync probe finished");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Desync failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-[1440px] flex-col gap-6 p-4 md:p-6">
      <CornerFrame className="p-5">
        <div className="border-b-2 border-primary pb-3">
          <h1 className="text-[1.25rem] font-medium tracking-tight">H2 Desync</h1>
          <p className="text-muted-foreground mt-1 text-sm">Single-Packet / H2 Desync probes</p>
        </div>
      </CornerFrame>

      <Card>
        <CardHeader>
          <CardTitle>Single-Packet / H2 Desync</CardTitle>
          <CardDescription>CL.TE / TE.CL / H2.CL / H2.TE / pipelined race probes.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="desync-url">Target URL</Label>
            <Input id="desync-url" value={target} onChange={(e) => setTarget(e.target.value)} />
          </div>
          <Button onClick={run} disabled={busy}>
            {busy ? "Probing…" : "Run probes"}
          </Button>
          <ul className="space-y-2 text-sm">
            {results.map((r, i) => {
              const kind = r.Kind ?? r.kind ?? "?";
              const vuln = r.Vulnerable ?? r.vulnerable;
              const ev = r.Evidence ?? r.evidence ?? r.Error ?? r.error ?? "";
              return (
                <li key={i} className="rounded-md border border-border px-3 py-2">
                  <span className="font-medium">{kind}</span>
                  {vuln ? <span className="text-destructive ml-2">vulnerable</span> : <span className="text-muted-foreground ml-2">clean/inconclusive</span>}
                  {ev ? <div className="text-muted-foreground mt-1 font-mono text-xs">{ev}</div> : null}
                </li>
              );
            })}
          </ul>
        </CardContent>
      </Card>
    </div>
  );
}
