// Shared button primitive — Daylight Ledger visual system (venues/broker-cards
// design rev 2, §D). Replaces the app-wide ad hoc `.btn`/`.btn-primary` call
// sites (Task 7) with one component so variant semantics — what "primary"
// means, what "danger" requires — live in exactly one place instead of being
// reinvented (and drifting) per call site.
//
// "Bronze = all UI state" (global.css) applies here too: --accent is the ONLY
// color meaning active/primary. --danger is reserved for LIVE/kill/reject —
// danger is always an outline, never a fill (a signal, not a surface).
import { forwardRef, useEffect, useRef, useState, type ButtonHTMLAttributes, type CSSProperties, type MouseEvent } from "react";
import { HoverButton } from "./HoverButton";

export type ButtonVariant = "primary" | "neutral" | "danger" | "quiet";
export type ButtonSize = "sm" | "md";

const CONFIRM_TIMEOUT_MS = 3000;

// Default hover overlay per variant, mirroring the resting look each
// .btn-<variant> class declares in global.css. Applied via HoverButton's
// inline-overlay mechanism (not a plain CSS `:hover` rule) because several
// call sites (TopBar's arm/disarm chip, SettingsModal's nav rows) set a
// PERMANENT inline background/border for their own state color — a CSS
// `:hover` selector can never win against that regardless of specificity,
// which is HoverButton's whole reason to exist (see its own doc comment).
// Those call sites pass their own `hoverStyle` to override this default.
const DEFAULT_HOVER: Record<ButtonVariant, CSSProperties> = {
  // Deepen the fill without hardcoding a "darker accent" hex (light/dark
  // accents differ) — an inset black overlay darkens whichever accent is
  // currently active, in either palette.
  primary: { boxShadow: "inset 0 0 0 999px rgba(0,0,0,.14)" },
  neutral: { background: "var(--surface)", borderColor: "var(--text-muted)" },
  // Faint danger tint, same family as .chip-live's rgba wash — never a solid
  // fill (red stays a signal, not a surface).
  danger: { background: "rgba(168,30,48,.08)" },
  quiet: { color: "var(--text)", textDecoration: "underline" },
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /**
   * Two-click confirm, encapsulating the pattern every destructive action
   * used to reimplement by hand (VenuesSection's Remove/Reset/Restart,
   * OrderSettingsSection's Confirm reset): first click arms — the button
   * shows `confirmLabel` instead of its normal children — and a ~3s timeout
   * silently reverts it; a second click while armed fires `onClick` and
   * disarms. Armed state keeps the button's own variant treatment (a
   * danger-variant button stays danger-styled while armed — only the label
   * changes).
   */
  confirm?: boolean;
  /** Label shown while armed. Default "Sure?"; destructive sites should pass
   * their own short imperative (e.g. "Confirm remove"). */
  confirmLabel?: string;
  /** Square glyph-button padding. */
  iconOnly?: boolean;
  /** Disabled + a subtle busy affordance — no spinner theatrics. */
  loading?: boolean;
  /** Escape hatch: overrides the variant's default hover overlay. */
  hoverStyle?: CSSProperties;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  {
    variant = "neutral", size = "sm", confirm = false, confirmLabel, iconOnly = false, loading = false,
    hoverStyle, className, style, type, disabled, onClick, children, ...rest
  },
  ref,
) {
  const [armed, setArmed] = useState(false);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Clear any pending revert on unmount — a confirm-armed button that
  // unmounts mid-timeout (e.g. its row was removed) must not fire a stray
  // setState on a dead component.
  useEffect(() => () => {
    if (timeoutRef.current) clearTimeout(timeoutRef.current);
  }, []);

  const disarm = (): void => {
    setArmed(false);
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
  };

  const handleClick = (e: MouseEvent<HTMLButtonElement>): void => {
    if (confirm && !armed) {
      setArmed(true);
      timeoutRef.current = setTimeout(() => setArmed(false), CONFIRM_TIMEOUT_MS);
      return;
    }
    if (confirm && armed) disarm();
    onClick?.(e);
  };

  const isDisabled = !!(disabled || loading);
  const cls = [
    "btn",
    variant !== "neutral" ? `btn-${variant}` : null,
    size === "md" ? "btn-md" : null,
    iconOnly ? "btn-icon" : null,
    loading ? "btn-loading" : null,
    className,
  ].filter(Boolean).join(" ");

  return (
    <HoverButton
      ref={ref}
      type={type ?? "button"}
      className={cls}
      disabled={isDisabled}
      onClick={handleClick}
      // `var(--btn-transition)` (global.css), not a literal transition
      // string: HoverButton's hover overlay is itself inline, and an inline
      // style always beats a plain CSS rule regardless of specificity — so a
      // literal value here would silently ignore prefers-reduced-motion.
      // Reading a custom property that a `@media (prefers-reduced-motion)`
      // block redefines to `none` lets this inline declaration still track
      // the media query.
      style={{ transition: "var(--btn-transition)", ...style }}
      hoverStyle={hoverStyle ?? DEFAULT_HOVER[variant]}
      {...rest}
    >
      {confirm && armed ? (confirmLabel ?? "Sure?") : children}
    </HoverButton>
  );
});
