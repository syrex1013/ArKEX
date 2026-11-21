import { t } from "@/i18n";

import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "ArKex",
  version: packageJson.version,
  copyright: `© ${currentYear}, ArKex (ARTEX lineage).`,
  meta: {
    title: t("ArKex — Autonomous Penetration Testing Console"),
    description: t("Upgraded ARTEX console: dual-graph autonomous penetration testing"),
  },
};
