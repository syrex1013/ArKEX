"use client";

import * as React from "react";

import { HardDriveIcon } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { t } from "@/i18n";
import { api } from "@/lib/api";

type Breakdown = {
  pg_bytes: number;
  traffic_bytes: number;
  scratch_bytes: number;
  blob_bytes: number;
  total_gb: number;
  max_disk_gb: number;
  scratch_quota_mb: number;
  compression_ratio: number;
  saved_bytes: number;
};

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1 << 20) return `${(n / 1024).toFixed(1)} KiB`;
  if (n < 1 << 30) return `${(n / (1 << 20)).toFixed(1)} MiB`;
  return `${(n / (1 << 30)).toFixed(2)} GiB`;
}

export function StorageGovernorCard() {
  const [data, setData] = React.useState<Breakdown | null>(null);
  const [err, setErr] = React.useState("");

  React.useEffect(() => {
    let alive = true;
    api.systemStorage().then((b) => {
        if (alive) setData(b);
      })
      .catch((e: Error) => {
        if (alive) setErr(e.message || "unavailable");
      });
    return () => {
      alive = false;
    };
  }, []);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <HardDriveIcon className="size-4" />
          {t("Storage Governor")}
        </CardTitle>
        <CardDescription>
          {t("ArKex disk budget: Zstd traffic blobs, delta LLM turns, scratch quotas.")}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        {err && <p className="text-muted-foreground">{err}</p>}
        {!err && !data && <p className="text-muted-foreground">{t("Loading…")}</p>}
        {data && (
          <dl className="grid gap-2 sm:grid-cols-2">
            <div>
              <dt className="text-muted-foreground">{t("Total footprint")}</dt>
              <dd className="font-medium tabular-nums">{data.total_gb.toFixed(3)} GB</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t("Cap")}</dt>
              <dd className="font-medium tabular-nums">{data.max_disk_gb} GB</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t("PostgreSQL")}</dt>
              <dd className="tabular-nums">{fmtBytes(data.pg_bytes)}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t("Traffic store")}</dt>
              <dd className="tabular-nums">{fmtBytes(data.traffic_bytes)}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t("Task scratch")}</dt>
              <dd className="tabular-nums">{fmtBytes(data.scratch_bytes)}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t("Scratch quota")}</dt>
              <dd className="tabular-nums">{data.scratch_quota_mb} MiB / intent</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t("Compression ratio")}</dt>
              <dd className="tabular-nums">
                {data.compression_ratio > 0 ? data.compression_ratio.toFixed(3) : "—"}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t("Bytes saved")}</dt>
              <dd className="tabular-nums">{fmtBytes(data.saved_bytes)}</dd>
            </div>
          </dl>
        )}
      </CardContent>
    </Card>
  );
}
