"use client";

import { ArchiveIcon } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { t } from "@/i18n";

/** Operator instructions for restoring companies/tasks/findings from a prior ARTEX dump. */
export function ImportArtexCard() {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <ArchiveIcon className="size-4" />
          {t("Import prior ARTEX")}
        </CardTitle>
        <CardDescription>
          {t(
            "Restore engagements (tasks), findings, companies, traffic metadata, and chat from a previous ARTEX backup into this Docker stack.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <ol className="list-decimal space-y-2 pl-5 text-muted-foreground">
          <li>
            {t("On the old host run:")}{" "}
            <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">scripts/backup.sh --with-data</code>
          </li>
          <li>
            {t("Copy")}{" "}
            <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">backups/artex-db-*.sql.gz</code>,{" "}
            <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">backups/artex-data-*.tar.gz</code>,{" "}
            {t("and")} <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">keys/</code> {t("here.")}
          </li>
          <li>
            <code className="block whitespace-pre-wrap rounded bg-muted px-2 py-2 font-mono text-xs">
              {`scripts/import-artex.sh \\
  --db backups/artex-db-<ts>.sql.gz \\
  --data backups/artex-data-<ts>.tar.gz \\
  --keys /path/to/old/keys`}
            </code>
          </li>
          <li>
            <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
              COMPOSE_PROJECT_NAME=arkex-local docker compose -f docker-compose.yml -f docker-compose.local.yml up -d
              --build artex
            </code>
          </li>
        </ol>
        <p className="text-muted-foreground text-xs">
          {t(
            "Warning: import drops and recreates POSTGRES_DB. Engagements in ARTEX are tasks; findings/companies/traffic come with the dump.",
          )}
        </p>
      </CardContent>
    </Card>
  );
}
