"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { AppearanceControls, useAppearance } from "@/components/appearance";
import { WorkspaceProvider, useWorkspace } from "@/components/workspace";

const groups = [
  {
    label: "Functions",
    items: [
      ["/engagements/", "Engagements", "◇"],
      ["/reports/", "Reports", "▤"],
      ["/discussion/", "Team Discussion", "▱"],
      ["/account/", "Account", "○"],
      ["/", "Dashboard", "⌂"],
      ["/tasks/", "Tasks", "☷"],
      ["/findings/", "Findings", "◈"],
      ["/traffic/", "Traffic", "≈"],
      ["/assets/", "Assets", "▦"],
      ["/lab/", "0-Day Lab", "△"],
    ],
  },
  {
    label: "System",
    items: [
      ["/approvals/", "Approval Records", "✓"],
      ["/logs/", "Logs", "≡"],
      ["/settings/", "System Settings", "⚙"],
    ],
  },
];

export default function MainLayout({ children }: { children: React.ReactNode }) {
  return <WorkspaceProvider><WorkspaceShell>{children}</WorkspaceShell></WorkspaceProvider>;
}

function WorkspaceShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname().replace(/\/$/, "");
  const [menuOpen, setMenuOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const { engagements, selected, select, loading, error } = useWorkspace();
  const appearance = useAppearance();
  const { preferences } = appearance;

  return (
    <div className="app-shell" data-sidebar={preferences.sidebar} data-collapse={preferences.collapse} data-collapsed={collapsed} data-content={preferences.content} data-navbar={preferences.navbar}>
      <aside id="app-sidebar" className="app-sidebar" data-open={menuOpen}>
        <Link href="/" className="app-brand" onClick={() => setMenuOpen(false)}>
          <span className="app-mark" aria-hidden="true">AK</span>
          <span className="app-brand-text">ArKex</span>
        </Link>
        <nav aria-label="Main navigation">
          {groups.map(({ label, items }) => (
            <div key={label} className="nav-group">
              <p>{label}</p>
              {items.map(([href, title, icon]) => (
                <Link key={href} href={href} aria-label={title} title={title} aria-current={pathname === href.slice(0, -1) ? "page" : undefined} onClick={() => setMenuOpen(false)}>
                  <span className="nav-icon" aria-hidden="true">{icon}</span>
                  <span className="nav-title">{title}</span>
                </Link>
              ))}
            </div>
          ))}
        </nav>
      </aside>
      <div className="app-content">
        <header className="app-header">
          <button type="button" className="btn-secondary app-menu-button" aria-controls="app-sidebar" aria-expanded={menuOpen} onClick={() => setMenuOpen(!menuOpen)}>
            Menu
          </button>
          <button type="button" className="btn-secondary app-collapse-button" aria-controls="app-sidebar" aria-expanded={!collapsed} onClick={() => setCollapsed(!collapsed)}>
            {collapsed ? "Expand sidebar" : "Collapse sidebar"}
          </button>
          <label className="app-engagement">
            <span>Engagement</span>
            <select aria-label="Engagement" value={selected} onChange={(e) => select(e.target.value)} disabled={!engagements.length}>
              <option value="">{loading ? "Loading…" : error ? "Unavailable" : engagements.length ? "Select engagement" : "No engagements"}</option>
              {engagements.map(({ id, title }) => <option key={id} value={id}>{title}</option>)}
            </select>
          </label>
          <div className="app-header-actions">
            <PageSearch />
            <AppearanceControls appearance={appearance} />
            <Link href="/account/" className="app-account">Local session</Link>
          </div>
        </header>
        <main>{children}</main>
      </div>
    </div>
  );
}

function PageSearch() {
  const dialog = useRef<HTMLDialogElement>(null);
  const [query, setQuery] = useState("");
  const matches = groups.flatMap(({ items }) => items).filter(([, title]) => title.toLowerCase().includes(query.trim().toLowerCase()));

  useEffect(() => {
    function openSearch(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        if (!dialog.current?.open) dialog.current?.showModal();
      }
    }
    window.addEventListener("keydown", openSearch);
    return () => window.removeEventListener("keydown", openSearch);
  }, []);

  return (
    <>
      <button className="btn-secondary" type="button" onClick={() => dialog.current?.showModal()}>Search</button>
      <dialog ref={dialog} className="app-search card" aria-label="Find a page" onClose={() => setQuery("")}>
        <div className="flex items-center gap-2">
          <input type="search" aria-label="Search pages" placeholder="Find a page…" value={query} onChange={(e) => setQuery(e.target.value)} />
          <button className="btn-secondary" type="button" onClick={() => dialog.current?.close()}>Close</button>
        </div>
        {matches.length ? (
          <ul>
            {matches.map(([href, title]) => (
              <li key={href}><Link href={href} onClick={() => dialog.current?.close()}>{title}</Link></li>
            ))}
          </ul>
        ) : <p className="workspace-muted">No pages found.</p>}
      </dialog>
    </>
  );
}
