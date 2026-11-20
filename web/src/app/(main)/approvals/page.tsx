"use client";

import { useEffect, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { api, type Intercept } from "@/lib/api";

export default function ApprovalsPage() {
  const [intercepts, setIntercepts] = useState<Intercept[]>([]);

  const load = () => api.listIntercepts().then(setIntercepts).catch(console.error);

  useEffect(() => {
    load();
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
  }, []);

  const approve = async (id: string) => {
    await api.approveIntercept(id);
    load();
  };
  const deny = async (id: string) => {
    await api.denyIntercept(id);
    load();
  };

  return (
    <div className="page-container" style={{ paddingTop: 32, paddingBottom: 64 }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 32 }}>
        <h1 style={{ fontSize: "var(--text-h1)" }}>Governance & Approvals</h1>
        {intercepts.length > 0 && (
          <span style={{
            background: "var(--tv-primary)",
            color: "#fff",
            borderRadius: "var(--radius-sm)",
            padding: "2px 10px",
            fontSize: "var(--text-label-sm)",
            fontWeight: 700,
          }}>
            {intercepts.length} pending
          </span>
        )}
      </div>

      {intercepts.length === 0 ? (
        <div className="card" style={{ padding: 48, textAlign: "center", color: "var(--tv-muted)" }}>
          No pending approvals.
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
          <AnimatePresence>
            {intercepts.map((i) => (
              <motion.div
                key={i.id}
                initial={{ opacity: 0, y: 6 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -6 }}
                transition={{ duration: 0.22, ease: [0.16, 1, 0.3, 1] }}
                style={{
                  border: "1px solid var(--tv-primary)",
                  borderRadius: "var(--radius-md)",
                  padding: 20,
                  background: "var(--tv-neutral)",
                }}
              >
                <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between" }}>
                  <div style={{ flex: 1 }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 8 }}>
                      <span style={{
                        fontSize: "var(--text-label-sm)",
                        background: "#fff0f0",
                        color: "var(--tv-primary)",
                        border: "1px solid var(--tv-primary)",
                        borderRadius: 2,
                        padding: "0 8px",
                        fontWeight: 600,
                        textTransform: "uppercase",
                      }}>{i.kind}</span>
                      <span style={{ fontSize: "var(--text-body-sm)", color: "var(--tv-muted)" }}>
                        {new Date(i.created_at).toLocaleString()}
                      </span>
                    </div>
                    <pre style={{ margin: 0, fontSize: 12, maxHeight: 120, overflow: "auto" }}>
                      {typeof i.payload === "string" ? i.payload : JSON.stringify(i.payload, null, 2)}
                    </pre>
                  </div>
                  <div style={{ display: "flex", flexDirection: "column", gap: 8, marginLeft: 16 }}>
                    <button className="btn-primary" style={{ width: 90 }} onClick={() => approve(i.id)}>
                      Approve
                    </button>
                    <button
                      className="btn-secondary"
                      style={{ width: 90, color: "var(--tv-error)", borderColor: "var(--tv-error)" }}
                      onClick={() => deny(i.id)}
                    >
                      Deny
                    </button>
                  </div>
                </div>
              </motion.div>
            ))}
          </AnimatePresence>
        </div>
      )}
    </div>
  );
}
