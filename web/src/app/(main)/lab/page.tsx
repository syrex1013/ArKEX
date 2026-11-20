"use client";

import * as React from "react";
import { useState } from "react";

import { CornerFrame } from "@/components/threatvector/corner-frame";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Activity, Radio, ShieldAlert, Target, Terminal, Zap } from "lucide-react";

export default function LabPage() {
  const [taskId, setTaskId] = useState("");

  const tools = [
    {
      title: "H2 Desync",
      description: "CL.TE / TE.CL / H2.CL / H2.TE / pipelined race probes",
      href: "/lab/desync",
      icon: Activity,
    },
    {
      title: "CPG / Taint",
      description: "Code Property Graph sink indexer over harvested source",
      href: "/lab/cpg",
      icon: Terminal,
    },
    {
      title: "AFL++ / Fuzzer",
      description: "Hybrid fuzz job launcher with Docker integration",
      href: "/lab/fuzzer",
      icon: Zap,
    },
    {
      title: "OAST",
      description: "Out-of-band application security testing payloads (S3 receipts)",
      href: "/lab/oast",
      icon: Radio,
    },
    {
      title: "Sliver C2",
      description: "Sliver C2 operator interface with beacons, sessions, implant builder",
      href: "/lab/sliver",
      icon: Target,
    },
    {
      title: "Mullvad VPN",
      description: "WireGuard egress management with relay picker and ping",
      href: "/lab/mullvad",
      icon: ShieldAlert,
    },
  ] as const;

  return (
    <div className="mx-auto flex w-full max-w-[1440px] flex-col gap-6 p-4 md:p-6">
      <CornerFrame className="p-5">
        <div className="border-b-2 border-primary pb-3">
          <h1 className="text-[1.25rem] font-medium tracking-tight">0-Day & Protocol Lab</h1>
          <p className="text-muted-foreground mt-1 text-sm">
            ArKex upgrade surface: CPG sinks, hybrid fuzz jobs, H2 desync probes, OAST payloads.
          </p>
        </div>
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="lab-task">Task ID (optional binding)</Label>
            <Input id="lab-task" value={taskId} onChange={(e) => setTaskId(e.target.value)} placeholder="numeric task id" />
          </div>
        </div>
      </CornerFrame>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {tools.map((tool) => {
          const Icon = tool.icon;
          return (
            <CornerFrame key={tool.href}>
              <a href={tool.href} className="block">
                <Card className="h-full transition-colors hover:bg-muted/50">
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                      <Icon className="size-5" />
                      {tool.title}
                    </CardTitle>
                    <CardDescription>{tool.description}</CardDescription>
                  </CardHeader>
                </Card>
              </a>
            </CornerFrame>
          );
        })}
      </div>
    </div>
  );
}
