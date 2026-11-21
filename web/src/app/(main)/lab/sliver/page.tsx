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

export default function SliverPage() {
  const [connected, setConnected] = useState(false);
  const [busy, setBusy] = useState(false);
  const [config, setConfig] = useState("");
  const [beacons, setBeacons] = useState<
    Array<{
      id: string;
      name?: string;
      hostname?: string;
      username?: string;
      os?: string;
      arch?: string;
      remote?: string;
      alive?: boolean;
      interval?: number;
      last_seen?: string;
    }>
  >([]);
  const [sessions, setSessions] = useState<
    Array<{
      id: string;
      name?: string;
      hostname?: string;
      username?: string;
      os?: string;
      remote?: string;
      alive?: boolean;
    }>
  >([]);
  const [execTarget, setExecTarget] = useState("");
  const [execCmd, setExecCmd] = useState("");
  const [execOut, setExecOut] = useState("");
  const [genOS, setGenOS] = useState("linux");
  const [genArch, setGenArch] = useState("amd64");
  const [genFormat, setGenFormat] = useState("exe");
  const [genBeacon, setGenBeacon] = useState(true);
  const [genC2, setGenC2] = useState("");
  const [genOut, setGenOut] = useState("");

  async function load() {
    try {
      const st = await api.c2Status();
      setConnected(st.connected);
      if (!st.connected) return;
      const [b, s] = await Promise.all([api.c2Beacons(), api.c2Sessions()]);
      setBeacons(b.beacons ?? []);
      setSessions(s.sessions ?? []);
    } catch {
      setConnected(false);
    }
  }

  async function connect() {
    setBusy(true);
    try {
      await api.c2Connect(config.trim() || undefined);
      toast.success("Sliver connected");
      await load();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Sliver connect failed");
    } finally {
      setBusy(false);
    }
  }

  async function runExec() {
    if (!execTarget.trim() || !execCmd.trim()) return;
    setBusy(true);
    try {
      const [path, ...args] = execCmd.trim().split(/\s+/);
      const r = await api.c2Exec(execTarget.trim(), path, args);
      setExecOut(`$ ${execCmd}\nexit=${r.status}\n${r.stdout}${r.stderr ? `\n[stderr]\n${r.stderr}` : ""}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "exec failed");
    } finally {
      setBusy(false);
    }
  }

  async function killTarget(t: string) {
    try {
      await api.c2Kill(t);
      toast.success(`Killed ${t}`);
      await load();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "kill failed");
    }
  }

  async function runGenerate() {
    setBusy(true);
    try {
      const r = await api.c2Generate({
        goos: genOS,
        goarch: genArch,
        format: genFormat,
        is_beacon: genBeacon,
        c2_urls: genC2
          ? genC2
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean)
          : [],
      });
      setGenOut(r.path);
      toast.success("Implant built");
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "generate failed");
    } finally {
      setBusy(false);
    }
  }

  React.useEffect(() => {
    void load();
  }, []);

  return (
    <div className="mx-auto flex w-full max-w-[1440px] flex-col gap-6 p-4 md:p-6">
      <CornerFrame className="p-5">
        <div className="border-b-2 border-primary pb-3">
          <h1 className="text-[1.25rem] font-medium tracking-tight">Sliver C2</h1>
          <p className="text-muted-foreground mt-1 text-sm">Sliver C2 operator interface</p>
        </div>
      </CornerFrame>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between">
            <span>Sliver C2 Operator</span>
            <span className={connected ? "text-[#198754]" : "text-muted-foreground"}>
              {connected ? "connected" : "disconnected"}
            </span>
          </CardTitle>
          <CardDescription>
            Operator config from `sliver multiplayer new-operator --name arkex` (env ARKEX_SLIVER_CONFIG or
            data/sliver/arkex.operator.conf).
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-end gap-3">
            <div className="min-w-64 flex-1 space-y-1.5">
              <Label htmlFor="c2-config">Operator config path (optional override)</Label>
              <Input
                id="c2-config"
                value={config}
                onChange={(e) => setConfig(e.target.value)}
                placeholder="data/sliver/arkex.operator.conf"
              />
            </div>
            <Button onClick={connect} disabled={busy}>
              {connected ? "Reconnect" : "Connect"}
            </Button>
            {connected ? (
              <Button variant="outline" onClick={() => api.c2Disconnect().then(load)} disabled={busy}>
                Disconnect
              </Button>
            ) : null}
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <div className="space-y-2">
              <h3 className="text-sm font-medium">Beacons ({beacons.length})</h3>
              <ul className="max-h-64 space-y-1 overflow-auto text-xs font-mono">
                {beacons.map((b) => (
                  <li key={b.id} className="flex items-center justify-between rounded border border-border px-2 py-1.5">
                    <span className={b.alive ? "" : "text-muted-foreground line-through"}>
                      {b.name} · {b.hostname} · {b.os}/{b.arch} · {b.remote}
                    </span>
                    <span className="flex gap-1">
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          setExecTarget(`beacon:${b.id}`);
                        }}
                      >
                        use
                      </Button>
                      <Button type="button" size="sm" variant="ghost" onClick={() => killTarget(`beacon:${b.id}`)}>
                        rm
                      </Button>
                    </span>
                  </li>
                ))}
              </ul>
            </div>
            <div className="space-y-2">
              <h3 className="text-sm font-medium">Sessions ({sessions.length})</h3>
              <ul className="max-h-64 space-y-1 overflow-auto text-xs font-mono">
                {sessions.map((s) => (
                  <li key={s.id} className="flex items-center justify-between rounded border border-border px-2 py-1.5">
                    <span className={s.alive ? "" : "text-muted-foreground line-through"}>
                      {s.name} · {s.hostname} · {s.os} · {s.remote}
                    </span>
                    <span className="flex gap-1">
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          setExecTarget(`session:${s.id}`);
                        }}
                      >
                        use
                      </Button>
                      <Button type="button" size="sm" variant="ghost" onClick={() => killTarget(`session:${s.id}`)}>
                        kill
                      </Button>
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="exec-target">Target (session:id / beacon:id)</Label>
            <Input
              id="exec-target"
              value={execTarget}
              onChange={(e) => setExecTarget(e.target.value)}
              placeholder="beacon:xxxx"
            />
            <Label htmlFor="exec-cmd">Command</Label>
            <div className="flex gap-2">
              <Input
                id="exec-cmd"
                value={execCmd}
                onChange={(e) => setExecCmd(e.target.value)}
                placeholder="/bin/sh -c id"
              />
              <Button onClick={runExec} disabled={busy || !connected}>
                Run
              </Button>
            </div>
            {execOut ? (
              <pre className="max-h-64 overflow-auto rounded bg-muted p-3 font-mono text-xs whitespace-pre-wrap">
                {execOut}
              </pre>
            ) : null}
          </div>

          <div className="space-y-2 border-t border-border pt-4">
            <h3 className="text-sm font-medium">Implant builder</h3>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <div className="space-y-1.5">
                <Label>GOOS</Label>
                <Input value={genOS} onChange={(e) => setGenOS(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label>GOARCH</Label>
                <Input value={genArch} onChange={(e) => setGenArch(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label>Format</Label>
                <Select value={genFormat} onValueChange={setGenFormat}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="exe">exe</SelectItem>
                    <SelectItem value="shared-lib">shared-lib</SelectItem>
                    <SelectItem value="shared-service">shared-service</SelectItem>
                    <SelectItem value="shellcode">shellcode</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="flex items-end gap-2">
                <input
                  id="gen-beacon"
                  type="checkbox"
                  checked={genBeacon}
                  onChange={(e) => setGenBeacon(e.target.checked)}
                  className="size-4"
                />
                <Label htmlFor="gen-beacon">Beacon</Label>
              </div>
            </div>
            <div className="space-y-1.5">
              <Label>C2 URLs (comma-separated: mtls://…, wg://…, dns://…, https://…)</Label>
              <Input
                value={genC2}
                onChange={(e) => setGenC2(e.target.value)}
                placeholder="mtls://1.2.3.4:443,https://implant.example"
              />
            </div>
            <Button onClick={runGenerate} disabled={busy || !connected}>
              Build implant
            </Button>
            {genOut ? <p className="font-mono text-xs">{genOut}</p> : null}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
