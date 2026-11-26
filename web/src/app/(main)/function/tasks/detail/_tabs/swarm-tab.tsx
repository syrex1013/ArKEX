"use client";

import * as React from "react";

import { CheckIcon, RefreshCwIcon, SearchIcon, XIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { t } from "@/i18n";
import { api } from "@/lib/api";
import { setVisibleInterval } from "@/lib/polling";
import type { AttackPath, AttackPathStep, KnowledgeItem, SwarmAgentDetail, TaskSwarm } from "@/lib/types";

// Swarm tab: the long-horizon agent roster, the attack paths the chain agent
// proposes, and the task's project knowledge. Read-only polling plus human
// verdicts (validate/refute) on proposed path steps.
export function SwarmTab({ taskId }: { taskId: string }) {
  const [swarm, setSwarm] = React.useState<TaskSwarm | null>(null);
  const [pathDetails, setPathDetails] = React.useState<Record<number, { path: AttackPath; steps: AttackPathStep[] }>>(
    {},
  );
  const [knowledge, setKnowledge] = React.useState<KnowledgeItem[] | null>(null);
  const [query, setQuery] = React.useState("");
  const [error, setError] = React.useState("");
  const [busyStep, setBusyStep] = React.useState("");

  // Agent-level explanation keyed by roster id: current intent, last durable
  // result and why the agent is not advancing.
  const agentDetails = React.useMemo(() => {
    const map = new Map<number, SwarmAgentDetail>();
    for (const d of swarm?.agents_detail ?? []) map.set(d.agent_id, d);
    return map;
  }, [swarm]);

  const loadKnowledge = React.useCallback(
    (q: string) => {
      api
        .taskKnowledge(taskId, q)
        .then((r) => {
          setKnowledge(r);
          setError("");
        })
        .catch((e) => setError((e as Error).message));
    },
    [taskId],
  );

  const load = React.useCallback(() => {
    Promise.all([api.taskSwarm(taskId), api.taskPaths(taskId)])
      .then(async ([sw, paths]) => {
        setSwarm(sw);
        setError("");
        const details = await Promise.all(
          paths.map(async (p) => [p.id, await api.taskPath(taskId, p.id).catch(() => null)] as const),
        );
        const next: Record<number, { path: AttackPath; steps: AttackPathStep[] }> = {};
        for (const [pid, d] of details) {
          if (d) next[pid] = d;
        }
        setPathDetails(next);
      })
      .catch((e) => setError((e as Error).message));
  }, [taskId]);

  React.useEffect(() => {
    load();
    loadKnowledge("");
    // Agents run for hours; a light poll keeps roster/paths current.
    const stopPolling = setVisibleInterval(load, 15000);
    return stopPolling;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadKnowledge, load]);

  async function verdict(pathID: number, position: number, action: "validate" | "refute") {
    setBusyStep(`${pathID}:${position}`);
    try {
      if (action === "validate") {
        await api.validatePathStep(taskId, pathID, position);
      } else {
        await api.refutePathStep(taskId, pathID, position);
      }
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusyStep("");
    }
  }

  return (
    <div className="flex flex-col gap-4">
      {error ? <p className="text-destructive text-sm">{error}</p> : null}

      {swarm?.progress ? (
        <Card>
          <CardContent className="flex flex-wrap items-center gap-x-6 gap-y-2 pt-6">
            <div className="flex flex-col">
              <span className="text-muted-foreground text-xs">{t("Intents settled")}</span>
              <span className="font-medium text-lg">{swarm.progress.intents_settled}</span>
            </div>
            <div className="flex flex-col">
              <span className="text-muted-foreground text-xs">{t("Intents open")}</span>
              <span className="font-medium text-lg">{swarm.progress.intents_open}</span>
            </div>
            <div className="flex flex-col">
              <span className="text-muted-foreground text-xs">{t("Findings verified")}</span>
              <span className="font-medium text-lg">{swarm.progress.findings_verified}</span>
            </div>
            <div className="flex flex-col">
              <span className="text-muted-foreground text-xs">{t("Coverage gaps")}</span>
              <span className="font-medium text-lg">{swarm.progress.coverage_gaps}</span>
            </div>
            <div className="flex flex-col">
              <span className="text-muted-foreground text-xs">{t("Blocked agents")}</span>
              <span
                className={
                  swarm.progress.blocked_agents > 0
                    ? "font-medium text-amber-600 text-lg dark:text-amber-400"
                    : "font-medium text-lg"
                }
              >
                {swarm.progress.blocked_agents}
              </span>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>{t("Agent roster")}</CardTitle>
          <CardDescription>
            {t(
              "Durable agents assigned to this task. Each slot keeps its role and history across intents and restarts.",
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {swarm === null ? (
            <Skeleton className="h-24 w-full" />
          ) : swarm.agents.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>{t("No agents yet")}</EmptyTitle>
                <EmptyDescription>{t("The roster appears once the task starts running.")}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="flex flex-col divide-y">
              {swarm.agents.map((a) => {
                const detail = agentDetails.get(a.id);
                return (
                  <div key={a.id} className="flex flex-col gap-1 py-2.5">
                    <div className="flex flex-wrap items-center gap-2">
                      <code className="font-medium text-sm">{a.name}</code>
                      <Badge variant="outline">{t(a.role)}</Badge>
                      <Badge
                        variant={a.state === "working" ? "secondary" : a.state === "retired" ? "outline" : "default"}
                      >
                        {t(a.state)}
                      </Badge>
                      {detail?.wait_reason ? (
                        <Badge variant="outline" className="font-normal text-amber-600 dark:text-amber-400">
                          {detail.wait_reason}
                        </Badge>
                      ) : null}
                      <span className="ml-auto text-muted-foreground text-xs">
                        {t("{count} intents", { count: a.intents_done })} ·{" "}
                        {t("{count} findings", { count: a.findings })}
                      </span>
                    </div>
                    {detail?.current_intent ? (
                      <span className="text-muted-foreground text-xs">
                        {t("Working on")}: {detail.current_intent.title || `#${detail.current_intent.id}`} ·{" "}
                        {t(detail.current_intent.state)}
                      </span>
                    ) : a.current_intent_id ? (
                      <span className="text-muted-foreground text-xs">
                        {t("intent")} #{a.current_intent_id}
                      </span>
                    ) : null}
                    {detail?.last_checkpoint ? (
                      <span className="text-muted-foreground text-xs">
                        {t("Last durable result")}: {detail.last_checkpoint.phase}
                        {detail.last_checkpoint.next_step ? ` — ${detail.last_checkpoint.next_step}` : ""}
                      </span>
                    ) : null}
                  </div>
                );
              })}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("Attack paths")}</CardTitle>
          <CardDescription>
            {t(
              "Chains of findings and assets the chain agent proposes. Validate or refute the proposed steps to steer the next work.",
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {Object.keys(pathDetails).length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>{t("No attack paths")}</EmptyTitle>
                <EmptyDescription>
                  {t("Paths appear when findings can be chained into a larger impact.")}
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            Object.values(pathDetails)
              .sort((a, b) => b.path.score - a.path.score)
              .map(({ path, steps }) => (
                <div key={path.id} className="rounded-lg border p-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium text-sm">{path.title}</span>
                    <Badge
                      variant={
                        path.state === "validated" ? "default" : path.state === "refuted" ? "destructive" : "outline"
                      }
                    >
                      {t(path.state)}
                    </Badge>
                    <span className="text-muted-foreground text-xs">score {path.score.toFixed(1)}</span>
                  </div>
                  {path.rationale ? <p className="mt-1 text-muted-foreground text-xs">{path.rationale}</p> : null}
                  <div className="mt-2 flex flex-col gap-1.5">
                    {steps.map((st) => (
                      <div key={st.id} className="flex flex-wrap items-center gap-2 text-xs">
                        <span className="font-mono">
                          #{st.position} {t(st.edge)}
                        </span>
                        <span>
                          {st.kind} #{st.ref_id}
                        </span>
                        <Badge
                          variant={
                            st.status === "verified" ? "default" : st.status === "refuted" ? "destructive" : "outline"
                          }
                        >
                          {t(st.status)}
                        </Badge>
                        {st.status === "proposed" && st.ref_exists ? (
                          <span className="ml-auto flex gap-1">
                            <Button
                              size="sm"
                              variant="outline"
                              className="h-7 px-2"
                              disabled={busyStep === `${path.id}:${st.position}`}
                              onClick={() => verdict(path.id, st.position, "validate")}
                            >
                              <CheckIcon />
                              {t("Validate")}
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              className="h-7 px-2"
                              disabled={busyStep === `${path.id}:${st.position}`}
                              onClick={() => verdict(path.id, st.position, "refute")}
                            >
                              <XIcon />
                              {t("Refute")}
                            </Button>
                          </span>
                        ) : null}
                      </div>
                    ))}
                  </div>
                </div>
              ))
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t("Project knowledge")}</CardTitle>
          <CardDescription>
            {t(
              "What the swarm learned about this project across tasks: assets, dependencies, attempts, failed hypotheses and successful approaches.",
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              loadKnowledge(query.trim());
            }}
          >
            <Input
              placeholder={t("Search knowledge (e.g. a domain or vulnclass)")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            <Button type="submit" size="icon" variant="outline" aria-label={t("Search")}>
              <SearchIcon />
            </Button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              aria-label={t("Refresh")}
              onClick={() => {
                setQuery("");
                loadKnowledge("");
              }}
            >
              <RefreshCwIcon />
            </Button>
          </form>
          {knowledge === null ? (
            <Skeleton className="h-24 w-full" />
          ) : knowledge.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>{t("No knowledge yet")}</EmptyTitle>
                <EmptyDescription>{t("Facts accumulate as the swarm works on this project.")}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="flex flex-col divide-y">
              {knowledge.map((k) => (
                <div key={k.id} className="flex flex-col gap-1 py-2.5">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline">{t(k.kind)}</Badge>
                    <span className="font-medium text-sm">{k.title}</span>
                    <span className="ml-auto text-muted-foreground text-xs">
                      {t("confidence {n}", { n: k.confidence })} · {t("support {n}", { n: k.support })}
                      {k.confidence < 50 ? " · [INFERENCE]" : ""}
                    </span>
                  </div>
                  {k.body ? <p className="text-muted-foreground text-xs">{k.body}</p> : null}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
