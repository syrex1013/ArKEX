"use client";

import * as React from "react";

import Link from "next/link";

import { CheckIcon, ExternalLinkIcon, Loader2Icon, PlusIcon } from "lucide-react";
import { toast } from "sonner";

import { StatusBadge } from "@/components/status-badge";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { intlLocale, t } from "@/i18n";
import { api } from "@/lib/api";
import type {
  BriefTask,
  EngagementBrief,
  EngagementQueue,
  EngagementQueueItem,
  EngagementQueueKind,
  EngagementRecord,
  EngagementRecordKind,
  EngagementRecordStatus,
  EngagementWithStats,
  TimelineEntry,
} from "@/lib/types";

// Single-operator instance: review cursors are keyed by this constant, same
// default the backend uses when no operator name is supplied.
const OPERATOR = "operator";

const RECORD_KINDS: EngagementRecordKind[] = ["scope_revision", "operator_decision", "milestone", "brief", "note"];
const TIMELINE_KINDS = [...RECORD_KINDS, "task_outcome", "finding", "retest", "checkpoint", "coverage_gap"] as const;
const TIMELINE_PAGE = 100;
const QUEUE_KINDS: EngagementQueueKind[] = ["decision", "milestone", "finding", "approval", "path", "coverage_gap"];

function queueKindLabel(kind: EngagementQueueKind): string {
  switch (kind) {
    case "decision":
      return t("Decisions");
    case "milestone":
      return t("Milestones");
    case "finding":
      return t("Findings");
    case "approval":
      return t("Approvals");
    case "path":
      return t("Attack paths");
    case "coverage_gap":
      return t("Coverage gaps");
    default:
      return kind;
  }
}

function kindLabel(kind: string): string {
  switch (kind) {
    case "scope_revision":
      return t("Scope revision");
    case "operator_decision":
      return t("Decision");
    case "milestone":
      return t("Milestone");
    case "brief":
      return t("Brief");
    case "note":
      return t("Note");
    case "task_outcome":
      return t("Task outcome");
    case "finding":
      return t("Finding");
    case "retest":
      return t("Retest");
    case "checkpoint":
      return t("Checkpoint");
    case "coverage_gap":
      return t("Coverage gap");
    default:
      return kind;
  }
}

function fmtDateTime(value?: string): string {
  if (!value) return "—";
  const d = new Date(value);
  return Number.isNaN(d.getTime())
    ? "—"
    : d.toLocaleString(intlLocale, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function statusBadge(status: EngagementRecordStatus) {
  switch (status) {
    case "accepted":
      return (
        <Badge variant="outline" className="font-normal text-emerald-600 dark:text-emerald-400">
          {t("Accepted")}
        </Badge>
      );
    case "superseded":
      return (
        <Badge variant="secondary" className="font-normal">
          {t("Superseded")}
        </Badge>
      );
    case "done":
      return (
        <Badge variant="secondary" className="font-normal text-emerald-600 dark:text-emerald-400">
          {t("Done")}
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className="font-normal text-amber-600 dark:text-amber-400">
          {t("Open")}
        </Badge>
      );
  }
}

// One engagement record with optional status transitions. scope revisions and
// decisions are accepted (accepting supersedes the previous accepted one);
// milestones complete only when they carry completion criteria.
function RecordRow({
  rec,
  onStatus,
  busyId,
}: {
  rec: EngagementRecord;
  onStatus: (id: number, status: EngagementRecordStatus) => void;
  busyId: number | null;
}) {
  return (
    <div className="flex items-start gap-3 py-2">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline" className="font-normal">
            {kindLabel(rec.kind)}
          </Badge>
          {statusBadge(rec.status)}
          <span className="text-muted-foreground text-xs">{fmtDateTime(rec.created_at)}</span>
          {rec.due_at && (
            <span className="text-muted-foreground text-xs">
              · {t("due {date}", { date: fmtDateTime(rec.due_at) })}
            </span>
          )}
        </div>
        <p className="truncate font-medium text-sm">{rec.title}</p>
        {rec.body && <p className="line-clamp-3 whitespace-pre-wrap text-muted-foreground text-sm">{rec.body}</p>}
        {rec.criteria && (
          <p className="text-muted-foreground text-xs">
            {t("Criteria")}: {rec.criteria}
          </p>
        )}
        <span className="text-muted-foreground/70 text-xs">{rec.actor || "—"}</span>
      </div>
      {rec.status === "open" && rec.kind !== "note" && rec.kind !== "brief" && (
        <div className="flex shrink-0 flex-col gap-1">
          {(rec.kind === "scope_revision" || rec.kind === "operator_decision") && (
            <Button
              size="sm"
              variant="outline"
              disabled={busyId === rec.id}
              onClick={() => onStatus(rec.id, "accepted")}
            >
              {busyId === rec.id ? <Loader2Icon className="animate-spin" /> : <CheckIcon />} {t("Accept")}
            </Button>
          )}
          {rec.kind === "milestone" && (
            <Button
              size="sm"
              variant="outline"
              disabled={busyId === rec.id || !rec.criteria}
              title={rec.criteria ? undefined : t("Set completion criteria first")}
              onClick={() => onStatus(rec.id, "done")}
            >
              {busyId === rec.id ? <Loader2Icon className="animate-spin" /> : <CheckIcon />} {t("Mark done")}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

// One operator-queue row: kind, severity, owning task and where it came from.
function QueueRow({ item }: { item: EngagementQueueItem }) {
  return (
    <div className="flex items-start gap-3 py-2">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline" className="font-normal">
            {kindLabel(item.kind)}
          </Badge>
          {item.severity && <StatusBadge domain="severity" value={item.severity} />}
          <span className="text-muted-foreground text-xs">{fmtDateTime(item.created_at)}</span>
        </div>
        <p className="truncate font-medium text-sm">{item.title}</p>
        {item.detail && <p className="line-clamp-2 text-muted-foreground text-sm">{item.detail}</p>}
      </div>
      <div className="flex shrink-0 flex-col items-end gap-1">
        {item.task_id > 0 && (
          <span className="text-muted-foreground text-xs">
            {item.task_name || t("Task {id}", { id: item.task_id })}
          </span>
        )}
        {item.link && (
          <Button asChild size="sm" variant="ghost">
            <Link href={item.link}>
              {t("Open")} <ExternalLinkIcon />
            </Link>
          </Button>
        )}
      </div>
    </div>
  );
}

function OutcomeStrip({ findings }: { findings: EngagementBrief["severity"] }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Badge variant="secondary" className="font-normal">
        {t("{count} total", { count: findings.total })}
      </Badge>
      <Badge variant="outline" className="font-normal text-emerald-600 dark:text-emerald-400">
        {t("{count} confirmed", { count: findings.confirmed })}
      </Badge>
      <Badge variant="outline" className="font-normal text-blue-500">
        {t("{count} open", { count: findings.open })}
      </Badge>
      {(["critical", "high", "medium", "low"] as const).map((level) =>
        findings[level] ? (
          <span key={level} className="inline-flex items-center gap-1">
            <StatusBadge domain="severity" value={level} dot />
            <span className="text-muted-foreground text-xs tabular-nums">{findings[level]}</span>
          </span>
        ) : null,
      )}
    </div>
  );
}

function TaskOutcomeCard({ task }: { task: BriefTask }) {
  const o = task.outcome;
  return (
    <Card className="gap-3">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 font-medium text-sm">
          <Link href={task.link} className="hover:underline">
            {task.name || t("Untitled task")}
            <ExternalLinkIcon className="ml-1 inline size-3.5" />
          </Link>
          <span className="font-normal text-muted-foreground text-xs">{task.status}</span>
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-2">
        <OutcomeStrip findings={o.findings} />
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground text-xs">
          <span>
            {t("Retests")}: {o.retests.reproduced} {t("reproduced")} / {o.retests.fixed} {t("fixed")} /{" "}
            {o.retests.inconclusive} {t("inconclusive")}
          </span>
          <span>
            {t("Tokens")}: {(o.tokens.input_tokens + o.tokens.output_tokens).toLocaleString(intlLocale)}
          </span>
          <span>
            {t("Tool calls")}: {o.tool_calls}
          </span>
          <span>
            {t("Wall")}: {Math.round(o.wall_seconds / 60)} min
          </span>
        </div>
      </CardContent>
    </Card>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-2">
      <h3 className="font-semibold text-sm">{title}</h3>
      {children}
    </div>
  );
}

function Empty({ text }: { text: string }) {
  return <p className="text-muted-foreground text-sm">{text}</p>;
}

export function EngagementDetail({ engagement }: { engagement: EngagementWithStats }) {
  // Timeline
  const [entries, setEntries] = React.useState<TimelineEntry[]>([]);
  const [records, setRecords] = React.useState<EngagementRecord[]>([]);
  const [kindFilter, setKindFilter] = React.useState<string>("all");
  const [loading, setLoading] = React.useState(true);
  const [loadingMore, setLoadingMore] = React.useState(false);

  // Briefs
  const [brief, setBrief] = React.useState<EngagementBrief | null>(null);
  const [reviewBrief, setReviewBrief] = React.useState<EngagementBrief | null>(null);

  // Union operator queue: everything across the engagement that needs a human.
  const [queue, setQueue] = React.useState<EngagementQueue | null>(null);

  // Record composer
  const [composeOpen, setComposeOpen] = React.useState(false);
  const [cKind, setCKind] = React.useState<EngagementRecordKind>("note");
  const [cTitle, setCTitle] = React.useState("");
  const [cBody, setCBody] = React.useState("");
  const [cCriteria, setCCriteria] = React.useState("");
  const [cDue, setCDue] = React.useState("");
  const [saving, setSaving] = React.useState(false);

  const [busyId, setBusyId] = React.useState<number | null>(null);
  const [marking, setMarking] = React.useState(false);

  const reload = React.useCallback(async () => {
    const [tl, recs, b, rb, q] = await Promise.all([
      api.engagements.timeline(engagement.id, { limit: TIMELINE_PAGE }),
      api.engagements.records(engagement.id, { limit: TIMELINE_PAGE }),
      api.engagements.brief(engagement.id),
      api.engagements.brief(engagement.id, OPERATOR),
      api.engagements.queue(engagement.id),
    ]);
    setEntries(tl);
    setRecords(recs);
    setBrief(b.brief);
    setReviewBrief(rb.brief);
    setQueue(q);
  }, [engagement.id]);

  React.useEffect(() => {
    setLoading(true);
    reload()
      .catch((e) => toast.error(t("Load failed: {error}", { error: (e as Error).message })))
      .finally(() => setLoading(false));
  }, [reload]);

  async function loadMore() {
    if (entries.length === 0) return;
    setLoadingMore(true);
    try {
      const more = await api.engagements.timeline(engagement.id, {
        limit: TIMELINE_PAGE,
        since: entries[entries.length - 1].created_at,
      });
      setEntries((prev) => [...prev, ...more]);
    } catch (e) {
      toast.error(t("Load failed: {error}", { error: (e as Error).message }));
    } finally {
      setLoadingMore(false);
    }
  }

  async function saveRecord() {
    if (!cTitle.trim()) {
      toast.error(t("Please fill in the title"));
      return;
    }
    setSaving(true);
    try {
      await api.engagements.createRecord(engagement.id, {
        kind: cKind,
        title: cTitle.trim(),
        body: cBody.trim(),
        criteria: cKind === "milestone" ? cCriteria.trim() : undefined,
        due_at: cKind === "milestone" && cDue ? new Date(cDue).toISOString() : undefined,
        actor: OPERATOR,
      });
      toast.success(t("Record added"));
      setComposeOpen(false);
      setCTitle("");
      setCBody("");
      setCCriteria("");
      setCDue("");
      await reload();
    } catch (e) {
      toast.error(t("Save failed: {error}", { error: (e as Error).message }));
    } finally {
      setSaving(false);
    }
  }

  async function setRecordStatus(id: number, status: EngagementRecordStatus) {
    setBusyId(id);
    try {
      await api.engagements.updateRecord(id, status);
      await reload();
    } catch (e) {
      toast.error(t("Save failed: {error}", { error: (e as Error).message }));
    } finally {
      setBusyId(null);
    }
  }

  async function markReviewed() {
    setMarking(true);
    try {
      await api.engagements.review(engagement.id, OPERATOR);
      toast.success(t("Marked as reviewed"));
      await reload();
    } catch (e) {
      toast.error(t("Save failed: {error}", { error: (e as Error).message }));
    } finally {
      setMarking(false);
    }
  }

  const visibleEntries = entries.filter((en) => kindFilter === "all" || en.kind === kindFilter);
  const recordById = React.useMemo(() => new Map(records.map((r) => [r.id, r])), [records]);
  const rb = reviewBrief;

  return (
    <Tabs defaultValue="timeline" className="gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <TabsList>
          <TabsTrigger value="timeline">{t("Timeline")}</TabsTrigger>
          <TabsTrigger value="review">{t("Review queue")}</TabsTrigger>
          <TabsTrigger value="queue">{t("Operator queue")}</TabsTrigger>
          <TabsTrigger value="brief">{t("Brief")}</TabsTrigger>
        </TabsList>
        <div className="ml-auto flex items-center gap-2">
          <Select value={kindFilter} onValueChange={setKindFilter}>
            <SelectTrigger className="w-40" size="sm">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("All kinds")}</SelectItem>
              {TIMELINE_KINDS.map((k) => (
                <SelectItem key={k} value={k}>
                  {kindLabel(k)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button size="sm" variant="outline" onClick={() => setComposeOpen(true)}>
            <PlusIcon /> {t("Add record")}
          </Button>
        </div>
      </div>

      <TabsContent value="timeline" className="grid gap-2">
        {loading && <Loader2Icon className="mx-auto my-8 size-5 animate-spin text-muted-foreground" />}
        {!loading && visibleEntries.length === 0 && <Empty text={t("Nothing on the timeline yet.")} />}
        {!loading && visibleEntries.length > 0 && (
          <Card>
            <CardContent className="divide-y">
              {visibleEntries.map((en) => {
                const rec = en.source_kind === "record" ? recordById.get(en.source_id) : undefined;
                if (rec) {
                  return <RecordRow key={en.entry_id} rec={rec} onStatus={setRecordStatus} busyId={busyId} />;
                }
                return <TimelineRow key={en.entry_id} entry={en} />;
              })}
            </CardContent>
          </Card>
        )}
        {!loading && entries.length >= TIMELINE_PAGE && (
          <Button variant="ghost" size="sm" className="mx-auto" disabled={loadingMore} onClick={loadMore}>
            {loadingMore && <Loader2Icon className="animate-spin" />} {t("Load more")}
          </Button>
        )}
      </TabsContent>

      <TabsContent value="review" className="grid gap-4">
        <div className="flex items-center gap-2">
          <p className="text-muted-foreground text-sm">
            {rb?.review
              ? t("Changes since {date}", { date: fmtDateTime(rb.review.reviewed_at) })
              : t("No review recorded yet — everything below counts as new.")}
          </p>
          <Button size="sm" className="ml-auto" disabled={marking} onClick={markReviewed}>
            {marking && <Loader2Icon className="animate-spin" />} {t("Mark reviewed")}
          </Button>
        </div>

        <Section title={t("Active scope")}>
          {rb?.active_scope ? (
            <div className="grid gap-0.5">
              <p className="font-medium text-sm">{rb.active_scope.title}</p>
              {rb.active_scope.body && (
                <p className="whitespace-pre-wrap text-muted-foreground text-sm">{rb.active_scope.body}</p>
              )}
            </div>
          ) : (
            <Empty text={t("No accepted scope revision yet.")} />
          )}
        </Section>

        <Section title={t("Open decisions")}>
          {rb && rb.open_decisions.length > 0 ? (
            <Card>
              <CardContent className="divide-y">
                {rb.open_decisions.map((d) => (
                  <RecordRow key={d.id} rec={d} onStatus={setRecordStatus} busyId={busyId} />
                ))}
              </CardContent>
            </Card>
          ) : (
            <Empty text={t("No open decisions.")} />
          )}
        </Section>

        <Section title={t("Open milestones")}>
          {rb && rb.open_milestones.length > 0 ? (
            <Card>
              <CardContent className="divide-y">
                {rb.open_milestones.map((m) => (
                  <RecordRow key={m.id} rec={m} onStatus={setRecordStatus} busyId={busyId} />
                ))}
              </CardContent>
            </Card>
          ) : (
            <Empty text={t("No open milestones.")} />
          )}
        </Section>

        <Section title={t("New records since last review")}>
          {rb?.review && rb.recent_records.length > 0 && (
            <Card>
              <CardContent className="divide-y">
                {rb.recent_records.map((rec) => (
                  <RecordRow key={rec.id} rec={rec} onStatus={setRecordStatus} busyId={busyId} />
                ))}
              </CardContent>
            </Card>
          )}
          {rb?.review && rb.recent_records.length === 0 && <Empty text={t("Nothing new since your last review.")} />}
          {!rb?.review && <Empty text={t("Mark reviewed to start tracking what changed.")} />}
        </Section>
      </TabsContent>

      <TabsContent value="brief" className="grid gap-4">
        {brief ? (
          <>
            <p className="text-muted-foreground text-xs">
              {t("Generated {date}", { date: fmtDateTime(brief.generated_at) })}
            </p>
            <OutcomeStrip findings={brief.severity} />
            <Separator />
            <Section title={t("Active scope")}>
              {brief.active_scope ? (
                <div className="grid gap-0.5">
                  <p className="font-medium text-sm">{brief.active_scope.title}</p>
                  {brief.active_scope.body && (
                    <p className="whitespace-pre-wrap text-muted-foreground text-sm">{brief.active_scope.body}</p>
                  )}
                </div>
              ) : (
                <Empty text={t("No accepted scope revision yet.")} />
              )}
            </Section>
            <Section title={t("Decisions")}>
              {brief.open_decisions.length > 0 ? (
                <ul className="grid gap-1">
                  {brief.open_decisions.map((d) => (
                    <li key={d.id} className="text-sm">
                      <span className="text-amber-600 dark:text-amber-400">{t("Decision (open)")}</span>: {d.title}
                    </li>
                  ))}
                </ul>
              ) : (
                <Empty text={t("No open decisions.")} />
              )}
            </Section>
            <Section title={t("Milestones")}>
              {brief.open_milestones.length > 0 ? (
                <ul className="grid gap-1">
                  {brief.open_milestones.map((m) => (
                    <li key={m.id} className="text-muted-foreground text-sm">
                      <span className="text-foreground">{m.title}</span>
                      {m.due_at && <> · {t("due {date}", { date: fmtDateTime(m.due_at) })}</>}
                    </li>
                  ))}
                </ul>
              ) : (
                <Empty text={t("No open milestones.")} />
              )}
            </Section>
            <Section title={t("Tasks")}>
              {brief.tasks.length > 0 ? (
                <div className="grid gap-2 md:grid-cols-2">
                  {brief.tasks.map((task) => (
                    <TaskOutcomeCard key={task.task_id} task={task} />
                  ))}
                </div>
              ) : (
                <Empty text={t("No tasks in this engagement.")} />
              )}
            </Section>
            <Section title={t("Coverage gaps")}>
              {brief.coverage_gaps.length > 0 ? (
                <ul className="grid gap-1">
                  {brief.coverage_gaps.map((g) => (
                    <li key={`${g.task_id}:${g.surface}:${g.risk_area}`} className="text-sm">
                      <Link href={g.link} className="hover:underline">
                        {g.surface} · {g.risk_area}
                      </Link>
                      {g.reason && <span className="text-muted-foreground"> — {g.reason}</span>}
                    </li>
                  ))}
                </ul>
              ) : (
                <Empty text={t("No unresolved coverage gaps.")} />
              )}
            </Section>
          </>
        ) : (
          <Loader2Icon className="mx-auto my-8 size-5 animate-spin text-muted-foreground" />
        )}
      </TabsContent>

      <TabsContent value="queue" className="grid gap-4">
        {queue ? (
          <>
            <div className="flex flex-wrap items-center gap-2">
              {QUEUE_KINDS.map((k) => (
                <Badge key={k} variant={queue.counts[k] > 0 ? "secondary" : "outline"} className="font-normal">
                  {queueKindLabel(k)}: {queue.counts[k]}
                </Badge>
              ))}
            </div>
            {queue.items.length === 0 ? (
              <Empty text={t("Nothing waiting on a human in this engagement.")} />
            ) : (
              <Card>
                <CardContent className="divide-y">
                  {queue.items.map((item) => (
                    <QueueRow key={`${item.kind}:${item.id}`} item={item} />
                  ))}
                </CardContent>
              </Card>
            )}
          </>
        ) : (
          <Loader2Icon className="mx-auto my-8 size-5 animate-spin text-muted-foreground" />
        )}
      </TabsContent>

      <Sheet open={composeOpen} onOpenChange={setComposeOpen}>
        <SheetContent side="right" className="w-full data-[side=right]:sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>{t("Add record")}</SheetTitle>
            <SheetDescription>
              {t("Scope revisions, operator decisions, milestones and notes are kept append-only.")}
            </SheetDescription>
          </SheetHeader>
          <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-4">
            <div className="grid gap-4 py-4">
              <div className="grid gap-2">
                <Label>{t("Kind")}</Label>
                <Select value={cKind} onValueChange={(v) => setCKind(v as EngagementRecordKind)}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {RECORD_KINDS.map((k) => (
                      <SelectItem key={k} value={k}>
                        {kindLabel(k)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-2">
                <Label htmlFor="r-title">{t("Title")}</Label>
                <Input id="r-title" value={cTitle} onChange={(e) => setCTitle(e.target.value)} />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="r-body">{t("Body")}</Label>
                <Textarea id="r-body" value={cBody} onChange={(e) => setCBody(e.target.value)} />
              </div>
              {cKind === "milestone" && (
                <>
                  <div className="grid gap-2">
                    <Label htmlFor="r-due">{t("Due date")}</Label>
                    <Input id="r-due" type="datetime-local" value={cDue} onChange={(e) => setCDue(e.target.value)} />
                  </div>
                  <div className="grid gap-2">
                    <Label htmlFor="r-criteria">{t("Completion criteria")}</Label>
                    <Input
                      id="r-criteria"
                      placeholder={t("What proves this milestone is done")}
                      value={cCriteria}
                      onChange={(e) => setCCriteria(e.target.value)}
                    />
                  </div>
                </>
              )}
            </div>
            <div className="pt-2 pb-6">
              <Button onClick={saveRecord} disabled={saving}>
                {saving && <Loader2Icon className="animate-spin" />} {t("Create")}
              </Button>
            </div>
          </div>
        </SheetContent>
      </Sheet>
    </Tabs>
  );
}

function TimelineRow({ entry }: { entry: TimelineEntry }) {
  return (
    <div className="flex items-start gap-3 py-2">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline" className="font-normal">
            {kindLabel(entry.kind)}
          </Badge>
          <span className="text-muted-foreground text-xs">{fmtDateTime(entry.created_at)}</span>
        </div>
        <p className="truncate font-medium text-sm">{entry.title}</p>
        {entry.summary && (
          <p className="line-clamp-2 whitespace-pre-wrap text-muted-foreground text-sm">{entry.summary}</p>
        )}
      </div>
      {entry.task_id ? (
        <Button asChild size="sm" variant="ghost" className="shrink-0">
          <Link href={entry.link}>
            <ExternalLinkIcon /> {t("Task {id}", { id: entry.task_id })}
          </Link>
        </Button>
      ) : null}
    </div>
  );
}
