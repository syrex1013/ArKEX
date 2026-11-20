"use client";

import { motion } from "framer-motion";

interface CornerFrameProps extends React.HTMLAttributes<HTMLDivElement> {
  children: React.ReactNode;
  className?: string;
  animate?: boolean;
}

export function CornerFrame({ children, className = "", animate = false, ...rest }: CornerFrameProps) {
  const brackets = [
    { className: "corner-tl", style: { top: 0, left: 0, borderWidth: "1.5px 0 0 1.5px" } },
    { className: "corner-tr", style: { top: 0, right: 0, borderWidth: "1.5px 1.5px 0 0" } },
    { className: "corner-bl", style: { bottom: 0, left: 0, borderWidth: "0 0 1.5px 1.5px" } },
    { className: "corner-br", style: { bottom: 0, right: 0, borderWidth: "0 1.5px 1.5px 0" } },
  ];

  return (
    <div className={`relative ${className}`} {...rest}>
      {brackets.map((b) =>
        animate ? (
          <motion.span
            key={b.className}
            initial={{ width: 6, height: 6 }}
            animate={{ width: 14, height: 14 }}
            transition={{ duration: 0.28, ease: [0.16, 1, 0.3, 1] }}
            style={{
              position: "absolute",
              borderColor: "var(--tv-primary)",
              borderStyle: "solid",
              ...b.style,
            }}
          />
        ) : (
          <span
            key={b.className}
            style={{
              position: "absolute",
              width: 14,
              height: 14,
              borderColor: "var(--tv-primary)",
              borderStyle: "solid",
              ...b.style,
            }}
          />
        )
      )}
      {children}
    </div>
  );
}

export function LiveDot({ active = false }: { active?: boolean }) {
  if (!active) return <span className="status-dot status-dot-idle" />;
  return (
    <span style={{ position: "relative", display: "inline-block", width: 10, height: 10 }}>
      <motion.span
        style={{
          position: "absolute",
          inset: 0,
          borderRadius: "50%",
          background: "var(--tv-primary)",
          opacity: 0.3,
        }}
        animate={{ scale: [1, 2, 1] }}
        transition={{ duration: 2, repeat: Infinity, ease: "easeInOut" }}
      />
      <span
        className="status-dot status-dot-active"
        style={{ position: "absolute", top: 2, left: 2, width: 6, height: 6 }}
      />
    </span>
  );
}
