"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { motion } from "framer-motion";

const NAV = [
  { href: "/", label: "Dashboard" },
  { href: "/tasks", label: "Tasks" },
  { href: "/findings", label: "Findings" },
  { href: "/traffic", label: "Traffic" },
  { href: "/lab", label: "0-Day Lab" },
  { href: "/approvals", label: "Approvals" },
  { href: "/settings", label: "Settings" },
];

export default function MainLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();

  return (
    <div style={{ display: "flex", flexDirection: "column", minHeight: "100vh" }}>
      {/* Top Navigation */}
      <header
        style={{
          height: 56,
          background: "var(--tv-neutral)",
          borderBottom: "1px solid var(--tv-border)",
          display: "flex",
          alignItems: "center",
          padding: "0 24px",
          gap: 32,
          position: "sticky",
          top: 0,
          zIndex: 100,
        }}
      >
        {/* Logo mark */}
        <Link href="/" style={{ textDecoration: "none", display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ color: "var(--tv-primary)", fontWeight: 700, fontFamily: "var(--font-mono)", fontSize: 16 }}>
            [AK]
          </span>
          <span style={{ fontWeight: 600, color: "var(--tv-secondary)", fontSize: 14 }}>ArKex</span>
          <span style={{ fontSize: 11, color: "var(--tv-muted)", fontFamily: "var(--font-mono)" }}>2.0</span>
        </Link>

        {/* Desktop nav */}
        <nav
          style={{ display: "flex", alignItems: "center", gap: 4 }}
          aria-label="main navigation"
        >
          {NAV.map((item) => {
            const active = pathname === item.href || (item.href !== "/" && pathname.startsWith(item.href));
            return (
              <Link
                key={item.href}
                href={item.href}
                style={{
                  position: "relative",
                  padding: "0 12px",
                  height: 56,
                  display: "flex",
                  alignItems: "center",
                  textDecoration: "none",
                  fontSize: "var(--text-body-sm)",
                  fontWeight: active ? 600 : 400,
                  color: active ? "var(--tv-secondary)" : "var(--tv-muted)",
                  transition: "color 0.18s",
                }}
              >
                {item.label}
                {active && (
                  <motion.span
                    layoutId="tv-tab-indicator"
                    style={{
                      position: "absolute",
                      bottom: 0,
                      left: 0,
                      right: 0,
                      height: 2,
                      background: "var(--tv-primary)",
                    }}
                    transition={{ duration: 0.22, ease: [0.16, 1, 0.3, 1] }}
                  />
                )}
              </Link>
            );
          })}
        </nav>
      </header>

      {/* Page Content */}
      <main style={{ flex: 1, background: "var(--tv-neutral)" }}>
        {children}
      </main>

      {/* Mobile Bottom Nav */}
      <nav
        aria-label="mobile navigation"
        style={{
          display: "none",
          position: "fixed",
          bottom: 0,
          left: 0,
          right: 0,
          background: "var(--tv-neutral)",
          borderTop: "1px solid var(--tv-border)",
          padding: "8px 0",
          zIndex: 100,
        }}
        className="mobile-nav"
      >
        {NAV.slice(0, 5).map((item) => {
          const active = pathname === item.href || (item.href !== "/" && pathname.startsWith(item.href));
          return (
            <Link
              key={item.href}
              href={item.href}
              style={{
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                flex: 1,
                padding: "4px 0",
                fontSize: 11,
                fontWeight: active ? 600 : 400,
                color: active ? "var(--tv-primary)" : "var(--tv-muted)",
                textDecoration: "none",
              }}
            >
              {item.label}
            </Link>
          );
        })}
      </nav>

      <style>{`
        @media (max-width: 767px) {
          header nav { display: none !important; }
          .mobile-nav { display: flex !important; }
          main { padding-bottom: 60px; }
        }
      `}</style>
    </div>
  );
}
