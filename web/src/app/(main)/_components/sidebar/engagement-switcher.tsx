"use client";

import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  SidebarMenu,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import { t } from "@/i18n";
import { useEngagementSelection, useEngagements } from "@/hooks/use-engagement";

export function EngagementSwitcher() {
  const { engagements } = useEngagements();
  const [selectedId, setSelectedId] = useEngagementSelection();

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <Select value={selectedId || "0"} onValueChange={setSelectedId}>
          <SelectTrigger size="sm" className="w-full" aria-label={t("Engagement")}>
            <SelectValue placeholder={t("All engagements")} />
          </SelectTrigger>
          <SelectContent position="popper">
            <SelectGroup>
              <SelectItem value="0">{t("All engagements")}</SelectItem>
              {engagements.map((e) => (
                <SelectItem key={e.id} value={String(e.id)}>
                  {e.name}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
