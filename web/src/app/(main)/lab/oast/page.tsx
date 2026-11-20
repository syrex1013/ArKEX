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

export default function OastPage() {
  const [tag, setTag] = useState("ssrf");
  const [payload, setPayload] = useState("");
  const [busy, setBusy] = useState(false);

  async function mint() {
    setBusy(true);
    try {
      const r = await api.oastPayload(tag, "lab");
      setPayload(r.payload);
      toast.success("OAST payload minted");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "OAST mint failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-[1440px] flex-col gap-6 p-4 md:p-6">
      <CornerFrame className="p-5">
        <div className="border-b-2 border-primary pb-3">
          <h1 className="text-[1.25rem] font-medium tracking-tight">OAST</h1>
          <p className="text-muted-foreground mt-1 text-sm">Out-of-band application security testing payloads</p>
        </div>
      </CornerFrame>

      <Card>
        <CardHeader>
          <CardTitle>OAST payloads (S3 receipts)</CardTitle>
          <CardDescription>Mint callback hosts; ingest hits via POST /api/oast/callback.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="oast-tag">Tag</Label>
            <Input id="oast-tag" value={tag} onChange={(e) => setTag(e.target.value)} />
          </div>
          <Button onClick={mint} disabled={busy}>
            {busy ? "Minting…" : "Generate payload"}
          </Button>
          {payload ? <p className="font-mono text-sm break-all">{payload}</p> : null}
        </CardContent>
      </Card>
    </div>
  );
}
