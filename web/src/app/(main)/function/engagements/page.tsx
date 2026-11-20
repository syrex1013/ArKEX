"use client";

import * as React from "react";
import Link from "next/link";
import { toast } from "sonner";
import { ExternalLinkIcon, FolderOpenIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { StatusBadge } from "@/components/status-badge";
import { cn } from "@/lib/utils";
import { intlLocale, t } from "@/i18n";
import { api } from "@/lib/api";
import { useEngagementSelection, useEngagements } from "@/hooks/use-engagement";
import type { EngagementWithStats } from "@/lib/types";

type FormState = { name: string; description: string };
const emptyForm: FormState = { name: "", description: "" };

const SEVERITIES = ["critical", "high", "medium", "low"] as const;

// fmtDate renders the created_at ISO string as a short local date, or "—" when unset.
function fmtDate(value?: string): string {
  if (!value) return "—";
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleDateString(intlLocale);
}

export default function EngagementsPage() {
  const { engagements, reload } = useEngagements();
  const [selectedId, setSelectedId] = useEngagementSelection();

  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<EngagementWithStats | null>(null); // null = add mode
  const [form, setForm] = React.useState<FormState>(emptyForm);
  const [saving, setSaving] = React.useState(false);
  const [deleting, setDeleting] = React.useState<EngagementWithStats | null>(null);

  function setF(patch: Partial<FormState>) {
    setForm((f) => ({ ...f, ...patch }));
  }

  function openAdd() {
    setEditing(null);
    setForm(emptyForm);
    setOpen(true);
  }

  function openEdit(e: EngagementWithStats) {
    setEditing(e);
    setForm({ name: e.name, description: e.description ?? "" });
    setOpen(true);
  }

  async function saveForm() {
    if (!form.name.trim()) {
      toast.error(t("Please fill in the name"));
      return;
    }
    setSaving(true);
    try {
      if (editing) {
        await api.engagements.update(editing.id, form.name.trim(), form.description.trim());
        toast.success(t("Saved"));
      } else {
        await api.engagements.create(form.name.trim(), form.description.trim());
        toast.success(t("Engagement created"));
        setOpen(false);
      }
      reload();
    } catch (e) {
      toast.error(t("Save failed: {error}", { error: (e as Error).message }));
    } finally {
      setSaving(false);
    }
  }

  async function removeEngagement() {
    if (!deleting) return;
    try {
      await api.engagements.remove(deleting.id);
      toast.success(t("Deleted: {name}", { name: deleting.name }));
      if (selectedId === String(deleting.id)) setSelectedId("0");
      setDeleting(null);
      reload();
    } catch (e) {
      toast.error(t("Delete failed: {error}", { error: (e as Error).message }));
    }
  }

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">{t("Engagements")}</h1>
        <p className="text-muted-foreground text-sm">
          {t("Group tasks and findings per engagement · click a card to scope the task list")}
        </p>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <button
          type="button"
          onClick={openAdd}
          className="text-foreground/70 border-foreground/70 hover:bg-muted/60 hover:shadow-sm flex min-h-[116px] flex-col items-center justify-center gap-2 rounded-xl border border-dashed transition"
        >
          <PlusIcon className="size-6" />
          <span className="text-sm">{t("New engagement")}</span>
        </button>

        {engagements.map((e) => {
          const selected = selectedId === String(e.id);
          return (
            <Card
              key={e.id}
              onClick={() => setSelectedId(String(e.id))}
              className={cn(
                "hover:border-primary/60 cursor-pointer gap-3 transition hover:shadow-sm",
                selected && "border-primary ring-primary/30 ring-2",
              )}
            >
              <CardHeader>
                <div className="flex items-center gap-2">
                  <FolderOpenIcon className="text-muted-foreground size-4 shrink-0" />
                  <CardTitle className="truncate text-base">{e.name}</CardTitle>
                  {selected && (
                    <Badge className="ml-auto font-normal">{t("Active")}</Badge>
                  )}
                  <div className="ml-auto flex items-center gap-1" onClick={(ev) => ev.stopPropagation()}>
                    {!selected && (
                      <Button
                        size="icon"
                        variant="outline"
                        aria-label={t("Set as active engagement")}
                        title={t("Set as active engagement")}
                        onClick={() => setSelectedId(String(e.id))}
                      >
                        <FolderOpenIcon />
                      </Button>
                    )}
                    <Button
                      size="icon"
                      variant="outline"
                      aria-label={t("Edit")}
                      onClick={() => openEdit(e)}
                    >
                      <PencilIcon />
                    </Button>
                    <Button
                      size="icon"
                      variant="outline"
                      aria-label={t("Delete")}
                      onClick={() => setDeleting(e)}
                    >
                      <Trash2Icon className="text-destructive" />
                    </Button>
                  </div>
                </div>
              </CardHeader>
              <CardContent className="grid gap-3">
                <p className="text-muted-foreground line-clamp-2 min-h-[2lh] text-sm">
                  {e.description || t("No description")}
                </p>
                <div className="flex flex-wrap gap-1.5">
                  <Badge variant="secondary" className="font-normal">
                    {t("{count} tasks", { count: e.stats.tasks_total })}
                  </Badge>
                  {e.stats.tasks_running > 0 && (
                    <Badge variant="outline" className="text-blue-500 font-normal">
                      {t("{count} running", { count: e.stats.tasks_running })}
                    </Badge>
                  )}
                  {e.stats.tasks_done > 0 && (
                    <Badge variant="outline" className="text-emerald-500 font-normal">
                      {t("{count} done", { count: e.stats.tasks_done })}
                    </Badge>
                  )}
                  {e.stats.tasks_failed > 0 && (
                    <Badge variant="destructive" className="font-normal">
                      {t("{count} failed", { count: e.stats.tasks_failed })}
                    </Badge>
                  )}
                </div>
                <div className="flex flex-wrap items-center gap-1.5">
                  {SEVERITIES.map((level) => {
                    const count = e.stats.findings[level];
                    if (!count) return null;
                    return (
                      <span key={level} className="inline-flex items-center gap-1">
                        <StatusBadge domain="severity" value={level} dot />
                        <span className="text-muted-foreground text-xs tabular-nums">{count}</span>
                      </span>
                    );
                  })}
                  {SEVERITIES.every((level) => !e.stats.findings[level]) && (
                    <span className="text-muted-foreground text-xs">{t("No findings")}</span>
                  )}
                </div>
                <div
                  className="flex items-center justify-between"
                  onClick={(ev) => ev.stopPropagation()}
                >
                  <span className="text-muted-foreground text-xs">{fmtDate(e.created_at)}</span>
                  <Button asChild size="sm" variant="ghost">
                    <Link href="/function/tasks">
                      <ExternalLinkIcon /> {t("Open tasks")}
                    </Link>
                  </Button>
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      {engagements.length === 0 && (
        <div className="text-muted-foreground flex flex-col items-center gap-2 py-12 text-sm">
          <FolderOpenIcon className="size-8 opacity-50" />
          <p>{t("No engagements yet. Create one to group tasks and findings.")}</p>
        </div>
      )}

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent side="right" className="w-full data-[side=right]:sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>{editing ? editing.name : t("New engagement")}</SheetTitle>
            <SheetDescription>
              {t("An engagement groups related tasks and their findings.")}
            </SheetDescription>
          </SheetHeader>
          <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-4">
            <div className="grid gap-4 py-4">
              <div className="grid gap-2">
                <Label htmlFor="e-name">{t("Name")}</Label>
                <Input
                  id="e-name"
                  placeholder="Acme Corp pentest"
                  value={form.name}
                  onChange={(ev) => setF({ name: ev.target.value })}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="e-desc">{t("Description")}</Label>
                <Textarea
                  id="e-desc"
                  placeholder={t("Scope, notes, contacts…")}
                  value={form.description}
                  onChange={(ev) => setF({ description: ev.target.value })}
                />
              </div>
            </div>
            <div className="pt-2 pb-6">
              <Button onClick={saveForm} disabled={saving}>
                {editing ? t("Save") : t("Create")}
              </Button>
            </div>
          </div>
        </SheetContent>
      </Sheet>

      <AlertDialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("Delete engagement \"{name}\"?", { name: deleting?.name ?? "" })}
            </AlertDialogTitle>
            <AlertDialogDescription className="[overflow-wrap:anywhere]">
              {t(
                "Its {count} tasks will be detached and kept, but they will no longer belong to this engagement. This action cannot be undone.",
                { count: deleting?.stats.tasks_total ?? 0 },
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("Cancel")}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={(ev) => {
              ev.preventDefault();
              void removeEngagement();
            }}>
              {t("Delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
