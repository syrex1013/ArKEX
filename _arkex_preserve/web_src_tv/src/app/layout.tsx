import type { Metadata, Viewport } from "next";
import { GeistSans } from "geist/font/sans";
import { GeistMono } from "geist/font/mono";
import { GeistPixelSquare } from "geist/font/pixel/square";
import "@/styles/globals.css";

export const metadata: Metadata = {
  title: "ArKex 2.0 — Autonomous Pentesting Platform",
  description: "Next-generation autonomous penetration testing & 0-day research platform",
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${GeistSans.variable} ${GeistMono.variable} ${GeistPixelSquare.variable}`} suppressHydrationWarning>
      <body>{children}</body>
    </html>
  );
}
