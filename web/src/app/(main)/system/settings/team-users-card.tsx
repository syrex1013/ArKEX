"use client";

import * as React from "react";

import { UsersIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { t } from "@/i18n";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

type TeamUser = {
  id: string;
  username: string;
  role: string;
  disabled: boolean;
  builtin?: boolean;
};

export function TeamUsersCard() {
  const me = auth.getCurrentUser();
  const isAdmin = (me?.role ?? "admin") === "admin";
  const [users, setUsers] = React.useState<TeamUser[]>([]);
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [role, setRole] = React.useState("operator");
  const [busy, setBusy] = React.useState(false);

  const load = React.useCallback(() => {
    if (!isAdmin) return;
    api
      .users()
      .then((r) => setUsers(r.users ?? []))
      .catch(() => setUsers([]));
  }, [isAdmin]);

  React.useEffect(() => {
    load();
  }, [load]);

  if (!isAdmin) {
    return null;
  }

  async function onCreate(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.createUser(username.trim(), password, role);
      toast.success(t("User created"));
      setUsername("");
      setPassword("");
      load();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("Create failed"));
    } finally {
      setBusy(false);
    }
  }

  async function onDisable(id: string) {
    setBusy(true);
    try {
      await api.disableUser(id);
      toast.success(t("User disabled"));
      load();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("Disable failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <UsersIcon className="size-4" />
          {t("Team accounts")}
        </CardTitle>
        <CardDescription>
          {t("Shared instance login. Bootstrap admin stays ARTEX; add operators for the rest of the team.")}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        <ul className="space-y-2 text-sm">
          {users.map((u) => (
            <li key={u.id} className="flex items-center justify-between gap-3 rounded-md bg-muted/40 px-3 py-2">
              <div className="min-w-0">
                <div className="truncate font-medium">{u.username}</div>
                <div className="text-xs uppercase tracking-wide text-muted-foreground">
                  {u.role}
                  {u.builtin ? " · builtin" : ""}
                  {u.disabled ? " · disabled" : ""}
                </div>
              </div>
              {!u.builtin && !u.disabled ? (
                <Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => onDisable(u.id)}>
                  {t("Disable")}
                </Button>
              ) : null}
            </li>
          ))}
        </ul>

        <form onSubmit={onCreate} className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="team-username">{t("Username")}</Label>
            <Input id="team-username" value={username} onChange={(e) => setUsername(e.target.value)} required />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="team-role">{t("Role")}</Label>
            <Select value={role} onValueChange={setRole}>
              <SelectTrigger id="team-role">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="admin">admin</SelectItem>
                <SelectItem value="operator">operator</SelectItem>
                <SelectItem value="viewer">viewer</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="team-password">{t("Password")}</Label>
            <Input
              id="team-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              minLength={12}
            />
          </div>
          <div className="sm:col-span-2">
            <Button type="submit" disabled={busy}>
              {t("Add team user")}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
