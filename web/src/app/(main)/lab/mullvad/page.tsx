"use client";

import * as React from "react";
import { useState } from "react";

import { toast } from "sonner";

import { CornerFrame } from "@/components/threatvector/corner-frame";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { api } from "@/lib/api";
import { beginEgressAction } from "@/lib/egress-busy";

export default function MullvadPage() {
  const [servers, setServers] = useState<
    Array<{ hostname: string; ipv4?: string; country?: string; country_code?: string; city?: string; ping_ms?: number }>
  >([]);
  const [sel, setSel] = useState("");
  const [status, setStatus] = useState<{ wg_quick: boolean; active: boolean; interface: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");

  async function load(ping: boolean) {
    setBusy(true);
    const endEgress = ping ? beginEgressAction() : null;
    try {
      const [list, st] = await Promise.all([api.mullvadServers(ping), api.mullvadStatus()]);
      setServers(list.servers ?? []);
      setStatus(st);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Mullvad list failed");
    } finally {
      endEgress?.();
      setBusy(false);
    }
  }

  async function up() {
    if (!sel) return;
    setBusy(true);
    const endEgress = beginEgressAction();
    try {
      const r = await api.mullvadUp(sel);
      setNote(r.note ?? (r.ok ? `Interface up: ${r.conf_path}` : ""));
      const st = await api.mullvadStatus();
      setStatus(st);
      toast.success(r.ok ? "WireGuard up" : "Config written (apply with wg-quick)");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "bring-up failed");
    } finally {
      endEgress();
      setBusy(false);
    }
  }

  async function down() {
    setBusy(true);
    const endEgress = beginEgressAction();
    try {
      await api.mullvadDown();
      const st = await api.mullvadStatus();
      setStatus(st);
      toast.success("WireGuard down");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "teardown failed");
    } finally {
      endEgress();
      setBusy(false);
    }
  }

  React.useEffect(() => {
    if (servers.length === 0) void load(false);
  }, []);

  return (
    <div className="mx-auto flex w-full max-w-[1440px] flex-col gap-6 p-4 md:p-6">
      <CornerFrame className="p-5">
        <div className="border-b-2 border-primary pb-3">
          <h1 className="text-[1.25rem] font-medium tracking-tight">Mullvad VPN</h1>
          <p className="text-muted-foreground mt-1 text-sm">Mullvad WireGuard egress management</p>
        </div>
      </CornerFrame>

      <Card>
        <CardHeader>
          <CardTitle>Mullvad WireGuard Egress</CardTitle>
          <CardDescription>
            Relay picker with TCP-connect ping. Bring-up needs wg-quick + your Mullvad-registered private key
            (data/mullvad/private.key or ARKEX_MULLVAD_PRIVKEY).
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" onClick={() => load(false)} disabled={busy}>
              Refresh list
            </Button>
            <Button variant="outline" onClick={() => load(true)} disabled={busy}>
              Refresh with ping
            </Button>
            <Button onClick={up} disabled={busy || !sel}>
              Bring up
            </Button>
            <Button variant="outline" onClick={down} disabled={busy}>
              Tear down
            </Button>
            <span className="text-muted-foreground text-sm">
              {status
                ? status.active
                  ? `active: ${status.interface}`
                  : status.wg_quick
                    ? "wg-quick ready, down"
                    : "wg-quick unavailable (manual apply)"
                : ""}
            </span>
          </div>
          {note ? <p className="text-muted-foreground text-xs">{note}</p> : null}
          <div className="space-y-1.5">
            <Label>Server</Label>
            <Select value={sel} onValueChange={setSel}>
              <SelectTrigger className="w-full sm:w-96">
                <SelectValue placeholder={servers.length ? "choose server" : "load list first"} />
              </SelectTrigger>
              <SelectContent className="max-h-72">
                {servers.map((srv) => (
                  <SelectItem key={srv.hostname} value={srv.hostname}>
                    {srv.country_code?.toUpperCase()} · {srv.city ?? srv.country} · {srv.hostname}
                    {typeof srv.ping_ms === "number" && srv.ping_ms >= 0 ? ` · ${srv.ping_ms.toFixed(0)} ms` : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
