import { useEffect, useRef, useState } from "react";
import { nextSessionTransition, sessionAt, type Session } from "../render/chart/sessions";
import { useTheme } from "./ThemeProvider";
import type { Palette } from "../render/palette";

// Module-scope Intl.DateTimeFormat (built once, not per tick) — same idiom as
// render/chart/chartTheme.ts's ET_TICK formatters. hour12:false + timeZone handles
// EST/EDT (DST) automatically.
const ET_CLOCK = new Intl.DateTimeFormat("en-US", {
  hour12: false, timeZone: "America/New_York",
  hour: "2-digit", minute: "2-digit", second: "2-digit",
});

const SESSION_LABEL: Record<Session, string> = { pre: "PRE", rth: "RTH", post: "POST", closed: "CLOSED" };
const SESSION_ANNOUNCEMENT: Partial<Record<Session, string>> = {
  pre: "Pre-market is now open.",
  rth: "Market is now open.",
};

function announceSession(session: Session): void {
  const text = SESSION_ANNOUNCEMENT[session];
  if (!text || typeof SpeechSynthesisUtterance === "undefined") return;
  const synth = window.speechSynthesis;
  if (!synth) return;
  const utterance = new SpeechSynthesisUtterance(text);
  utterance.voice = synth.getVoices().find((voice) => voice.name === "Google US English" && voice.lang === "en-US") ?? null;
  utterance.lang = "en-US";
  utterance.rate = 0.90;
  utterance.pitch = 1.05;
  synth.speak(utterance);
}

function formatSessionCountdown(ms: number): string {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  const days = Math.floor(totalSeconds / 86_400);
  const hours = Math.floor((totalSeconds % 86_400) / 3_600);
  const minutes = Math.floor((totalSeconds % 3_600) / 60);
  const seconds = totalSeconds % 60;
  const clock = [hours, minutes, seconds].map((part) => part.toString().padStart(2, "0")).join(":");
  return days > 0 ? `${days}d ${clock}` : clock;
}

// sessionPre/Rth/Post/Closed in the palette are low-alpha chart-shading fills
// (sessionRth is "usually transparent") — not visible as a status dot, so this
// maps to the same visible status tokens LatencyReadout uses.
const sessionColor = (s: Session, p: Palette): string =>
  s === "rth" ? p.ok : s === "pre" ? p.accent : s === "post" ? p.warn : p.textMuted;

// A 1Hz React projection of the wall clock. Interval (not rAF) keeps this
// deterministic under fake timers, matching the rule in exec/useThrottledQuote.ts.
function useEtClock(): number {
  const [now, setNow] = useState<number>(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return now;
}

// Left-side ET clock + next-session countdown. Client-derived — no store, no props.
// sessionAt() is a client-side wall-clock classifier only (no holiday awareness);
// acceptable here since this is a glance indicator, not the order-gate source of
// truth (preChecks.ts has its own sessionAt call for that).
export function SessionClock(): JSX.Element {
  const { palette } = useTheme();
  const now = useEtClock();
  const session = sessionAt(now);
  const previousSession = useRef<Session | null>(null);
  useEffect(() => {
    if (previousSession.current !== null && previousSession.current !== session) announceSession(session);
    previousSession.current = session;
  }, [session]);
  const transition = nextSessionTransition(now);
  const countdown = formatSessionCountdown(transition.atMs - now);
  const nextLabel = SESSION_LABEL[transition.session];
  return (
    <div
      data-testid="session-clock"
      style={{ display: "inline-flex", alignItems: "center", gap: 6, whiteSpace: "nowrap" }}
      title={`Current time (US/Eastern); ${SESSION_LABEL[session]}; ${nextLabel} in ${countdown}`}
    >
      <span
        style={{ width: 7, height: 7, borderRadius: "50%", background: sessionColor(session, palette) }}
      />
      <span className="mono" style={{ fontSize: 12, color: palette.text }}>
        {ET_CLOCK.format(now)}
      </span>
      <span
        className="serif"
        style={{ fontSize: 9, letterSpacing: ".06em", textTransform: "uppercase", color: palette.textMuted }}
      >
        ET · {nextLabel} in {countdown}
      </span>
    </div>
  );
}
