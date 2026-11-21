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

export default function FuzzerPage() {
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<number | null>(null);

  async function run() {
    setBusy(true);
    try {
      const r = await api.labFuzz({ task_id: "lab", kind: "afl++", target });
      setJob(r.job_id);
      toast.success(`Fuzz job ${r.job_id}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Fuzz launch failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-[1440px] flex-col gap-6 p-4 md:p-6">
      <CornerFrame className="p-5">
        <div className="border-b-2 border-primary pb-3">
          <h1 className="text-[1.25rem] font-medium tracking-tight">AFL++ / Fuzzer</h1>
          <p className="text-muted-foreground mt-1 text-sm">Hybrid fuzz job launcher</p>
        </div>
      </CornerFrame>

      <Card>
        <CardHeader>
          <CardTitle>Hybrid Fuzzer</CardTitle>
          <CardDescription>Queues AFL++ container job when Docker is available.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="fuzz-target">Target ref</Label>
            <Input
              id="fuzz-target"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
              placeholder="binary path or harness id"
            />
          </div>
          <Button onClick={run} disabled={busy || !target.trim()}>
            {busy ? "Launching…" : "Launch fuzz job"}
          </Button>
          {job != null ? <p className="text-sm">Job ID: {job}</p> : null}
        </CardContent>
      </Card>
    </div>
  );
}
