"use client";

import { useEffect, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { api, type Engagement, type Intent } from "@/lib/api";
import { CornerFrame, LiveDot } from "@/components/corner-frame";

export default function TasksPage() {
  const [engagements, setEngagements] = useState<Engagement[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [intents, setIntents] = useState<Intent[]>([]);
  const [steerText, setSteerText] = useState("");

  useEffect(() => {
    api.listEngagements().then(setEngagements).catch(console.error);
  }, []);

  useEffect(() => {
    if (!selected) return;
    api.listIntents(selected).then(setIntents).catch(console.error);
    const interval = setInterval(() => {
      api.listIntents(selected).then(setIntents).catch(console.error);
    }, 2000);
    return () => clearInterval(interval);
  }, [selected]);

  const running = engagements.filter((e) => e.status === "running");
  const other = engagements.filter((e) => e.status !== "running");

  return (
    <div className="page-container" style={{ paddingTop: 32, paddingBottom: 64 }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 32 }}>
        <h1 style={{ fontSize: "var(--text-h1)" }}>Tasks</h1>
        <a href="/tasks/new" className="btn-primary">New Task</a>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: selected ? "320px 1fr" : "1fr", gap: 24 }}>
        {/* Engagement list */}
        <div>
          {running.length > 0 && (
            <div style={{ marginBottom: 24 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
                <LiveDot active />
                <span style={{ fontSize: "var(--text-label-sm)", fontWeight: 600, color: "var(--tv-primary)", textTransform: "uppercase", letterSpacing: "0.05em" }}>
                  Live
                </span>
              </div>
              {running.map((e) => (
                <EngagementCard key={e.id} engagement={e} selected={selected === e.id} onClick={() => setSelected(e.id)} />
              ))}
            </div>
          )}
          <div>
            <span style={{ fontSize: "var(--text-label-sm)", fontWeight: 600, color: "var(--tv-muted)", textTransform: "uppercase", letterSpacing: "0.05em", display: "block", marginBottom: 12 }}>
              All
            </span>
            {other.map((e) => (
              <EngagementCard key={e.id} engagement={e} selected={selected === e.id} onClick={() => setSelected(e.id)} />
            ))}
          </div>
        </div>

        {/* Intent worker lanes */}
        {selected && (
          <div>
            <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 16 }}>
              <h2 style={{ fontSize: "var(--text-h2)" }}>Worker Lanes</h2>
              <button className="btn-secondary" style={{ fontSize: 13 }} onClick={() => setSelected(null)}>Close</button>
            </div>

            {/* Steer control */}
            <div style={{ display: "flex", gap: 8, marginBottom: 20 }}>
              <input
                type="text"
                placeholder="Steer active worker..."
                value={steerText}
                onChange={(e) => setSteerText(e.target.value)}
                style={{ flex: 1 }}
              />
              <button
                className="btn-secondary"
                style={{ width: 80, flexShrink: 0 }}
                onClick={() => {
                  const running = intents.find((i) => i.status === "running");
                  if (running && steerText) {
                    api.steerWork(running.id, steerText).then(() => setSteerText("")).catch(console.error);
                  }
                }}
              >
                Steer
              </button>
            </div>

            <AnimatePresence>
              {intents.map((intent) => (
                <IntentCard
                  key={intent.id}
                  intent={intent}
                  onKill={() => api.killWork(intent.id).then(() => api.listIntents(selected).then(setIntents))}
                />
              ))}
            </AnimatePresence>
          </div>
        )}
      </div>
    </div>
  );
}

function EngagementCard({
  engagement,
  selected,
  onClick,
}: {
  engagement: Engagement;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      style={{
        display: "block",
        width: "100%",
        textAlign: "left",
        background: selected ? "var(--tv-surface-subtle)" : "var(--tv-neutral)",
        border: `1px solid ${selected ? "var(--tv-primary)" : "var(--tv-border)"}`,
        borderRadius: "var(--radius-md)",
        padding: "12px 16px",
        marginBottom: 8,
        cursor: "pointer",
        transition: "border-color 0.18s",
      }}
    >
      <div style={{ fontWeight: 500, fontSize: "var(--text-body-sm)", marginBottom: 4 }}>{engagement.title}</div>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        {engagement.status === "running" && <LiveDot active />}
        <span style={{ fontSize: "var(--text-label-sm)", color: "var(--tv-muted)" }}>{engagement.status}</span>
      </div>
    </button>
  );
}

function IntentCard({ intent, onKill }: { intent: Intent; onKill: () => void }) {
  const isRunning = intent.status === "running";
  return (
    <motion.div
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: -6 }}
      transition={{ duration: 0.22, ease: [0.16, 1, 0.3, 1] }}
      style={{
        border: `1px solid ${isRunning ? "var(--tv-primary)" : "var(--tv-border)"}`,
        borderRadius: "var(--radius-md)",
        padding: "12px 16px",
        marginBottom: 8,
        background: "var(--tv-neutral)",
      }}
    >
      <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between" }}>
        <div>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
            {isRunning && <LiveDot active />}
            <span style={{ fontWeight: 500, fontSize: "var(--text-body-sm)" }}>{intent.title}</span>
            <span style={{
              fontSize: "var(--text-label-sm)",
              background: "var(--tv-surface-subtle)",
              border: "1px solid var(--tv-border)",
              borderRadius: 2,
              padding: "0 6px",
              color: "var(--tv-muted)",
            }}>{intent.kind}</span>
          </div>
          <div style={{ fontSize: "var(--text-label-sm)", color: "var(--tv-muted)" }}>
            {intent.tokens.toLocaleString()} tokens
          </div>
        </div>
        {isRunning && (
          <button
            className="btn-secondary"
            style={{ fontSize: 12, height: 28, padding: "0 10px", color: "var(--tv-error)" }}
            onClick={onKill}
          >
            Kill
          </button>
        )}
      </div>
    </motion.div>
  );
}
