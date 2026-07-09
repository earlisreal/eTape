import { forwardRef, useState, type ButtonHTMLAttributes, type CSSProperties } from "react";

// Default overlay resolves app-wide via ThemeProvider/cssVars.ts, which
// publish --surface/--text on :root.
const DEFAULT_HOVER: CSSProperties = { background: "var(--surface)", color: "var(--text)" };
const TRANSITION = "background 120ms ease, color 120ms ease, border-color 120ms ease";

export interface HoverButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** Overrides the default hover overlay (e.g. TV-island callers use chrome.hover/chrome.text). */
  hoverStyle?: CSSProperties;
}

/**
 * <button> wrapper that owns its own hover state via useState, so it's safe
 * to render inside a .map() where hooks can't be called per-item.
 */
export const HoverButton = forwardRef<HTMLButtonElement, HoverButtonProps>(
  function HoverButton({ style, hoverStyle, disabled, onMouseEnter, onMouseLeave, ...rest }, ref) {
    const [hovered, setHovered] = useState(false);
    const overlay = !disabled && hovered ? (hoverStyle ?? DEFAULT_HOVER) : null;
    return (
      <button
        ref={ref}
        disabled={disabled}
        style={{ transition: TRANSITION, ...style, ...overlay }}
        onMouseEnter={(e) => {
          if (!disabled) setHovered(true);
          onMouseEnter?.(e);
        }}
        onMouseLeave={(e) => {
          setHovered(false);
          onMouseLeave?.(e);
        }}
        {...rest}
      />
    );
  },
);
