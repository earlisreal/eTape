import { useEffect, useLayoutEffect, useRef, type RefObject } from "react";
import { modalTracker } from "../modalTracker";

const layers: object[] = [];
const focusable = 'button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])';

/** Only the frontmost surface handles dismissal and modal keyboard focus. */
export function useDismissibleLayer(
  ref: RefObject<HTMLElement | null>, onClose: () => void,
  { enabled = true, modal = false, anchor = null, dismissible = true }: {
    enabled?: boolean; modal?: boolean; anchor?: HTMLElement | null; dismissible?: boolean;
  } = {},
): void {
  const close = useRef(onClose);
  const canDismiss = useRef(dismissible);
  useLayoutEffect(() => { close.current = onClose; canDismiss.current = dismissible; });

  useEffect(() => {
    if (!enabled) return;
    const layer = {};
    layers.push(layer);
    modalTracker.setOpen(true);
    const previous = document.activeElement;
    const isTop = () => layers.at(-1) === layer;
    const controls = () => Array.from(ref.current?.querySelectorAll<HTMLElement>(focusable) ?? [])
      .filter((el) => !el.closest('[hidden], [inert], [aria-hidden="true"]') && getComputedStyle(el).display !== "none" && getComputedStyle(el).visibility !== "hidden");
    if (modal && !ref.current?.contains(document.activeElement)) (controls()[0] ?? ref.current)?.focus();

    const onKey = (event: KeyboardEvent) => {
      if (!isTop() || event.defaultPrevented) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopImmediatePropagation();
        if (canDismiss.current) {
          close.current();
          if (!modal) (anchor ?? (previous instanceof HTMLElement ? previous : null))?.focus();
        }
      }
      if (modal && event.key === "Tab") {
        const items = controls();
        const first = items[0] ?? ref.current;
        const last = items.at(-1) ?? ref.current;
        if (!ref.current?.contains(document.activeElement) || document.activeElement === ref.current
          || event.shiftKey && document.activeElement === first || !event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          (event.shiftKey ? last : first)?.focus();
        }
      }
    };
    const onOutside = (event: Event) => {
      if (!modal && isTop() && canDismiss.current && event.target instanceof Node
        && !ref.current?.contains(event.target) && !anchor?.contains(event.target)) close.current();
    };
    window.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onOutside, true);
    document.addEventListener("touchstart", onOutside, true);
    return () => {
      const wasTop = isTop();
      layers.splice(layers.indexOf(layer), 1);
      modalTracker.setOpen(false);
      window.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onOutside, true);
      document.removeEventListener("touchstart", onOutside, true);
      if (modal && wasTop && previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, [ref, enabled, modal, anchor]);
}
