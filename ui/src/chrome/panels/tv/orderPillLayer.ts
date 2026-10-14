const selector = "[data-order-chip],[data-risk-chip]";
const layers = new WeakMap<HTMLElement, ReturnType<typeof createLayer>>();

function createLayer(host: HTMLElement) {
  const slot = document.createElement("div");
  slot.dataset.orderPillLayer = "";
  Object.assign(slot.style, { position: "absolute", inset: "0", pointerEvents: "none" });
  const pills = new Map<HTMLElement, { root: HTMLElement; visual: HTMLElement; html: string }>();
  const roots = new Set<HTMLElement>();
  let dragged: HTMLElement | null = null, hovered: HTMLElement | null = null, focused: HTMLElement | null = null;
  let dragRoot: HTMLElement | null = null;
  const locate = (target: EventTarget | null) => {
    const pill = target instanceof Element ? target.closest<HTMLElement>(selector) : null;
    return pill && pills.has(pill) ? pill : null;
  };
  const priority = () => {
    const active = [dragged, hovered, focused].find(pill => pill && pills.has(pill));
    for (const [pill, { visual }] of pills) {
      const zIndex = pill === active ? "11" : "1";
      if (visual.style.zIndex !== zIndex) visual.style.zIndex = zIndex;
      const outlined = pill === active && pill === focused;
      if (visual.style.outlineStyle !== (outlined ? "solid" : "none")) visual.style.outline = outlined ? "2px solid currentColor" : "none";
    }
  };
  const place = () => {
    const timer = host.querySelector("[data-testid='bar-close-timer']");
    // Each graphic interleaves with LWC's canvases; the slot creates no stacking context.
    if (timer ? slot.nextSibling !== timer : host.lastChild !== slot) host.insertBefore(slot, timer);
  };
  const sync = (root: HTMLElement) => {
    place();
    const sources = new Set(root.querySelectorAll<HTMLElement>(selector));
    for (const [pill, entry] of pills) if (entry.root === root && !sources.has(pill)) {
      entry.visual.remove(); pills.delete(pill);
    }
    const hostBox = host.getBoundingClientRect();
    for (const pill of sources) {
      let entry = pills.get(pill);
      const html = pill.outerHTML;
      if (!entry || entry.html !== html) {
        const visual = pill.cloneNode(true) as HTMLElement;
        for (const element of [visual, ...visual.querySelectorAll("*")]) {
          for (const attribute of [...element.attributes]) if (!["style", "disabled", "type"].includes(attribute.name)) element.removeAttribute(attribute.name);
        }
        visual.dataset.orderPillVisual = pill.closest<HTMLElement>("[data-order-group]")?.dataset.orderIds ?? pill.dataset.riskChip ?? "";
        visual.setAttribute("aria-hidden", "true");
        visual.setAttribute("inert", "");
        visual.style.pointerEvents = "none";
        for (const child of visual.querySelectorAll<HTMLElement>("*")) child.style.pointerEvents = "none";
        if (entry) entry.visual.replaceWith(visual); else slot.append(visual);
        entry = { root, visual, html }; pills.set(pill, entry);
      }
      let visible = true, opacity = 1;
      for (let ancestor: HTMLElement | null = pill; ancestor && ancestor !== host; ancestor = ancestor.parentElement) {
        const style = getComputedStyle(ancestor);
        if (style.display === "none") visible = false;
        if (ancestor !== pill) opacity *= style.opacity === "" ? 1 : Number(style.opacity);
      }
      const box = pill.getBoundingClientRect();
      Object.assign(entry.visual.style, { display: visible ? pill.style.display : "none", opacity: String(opacity),
        left: `${box.left - hostBox.left}px`, top: `${box.top - hostBox.top}px`, right: "auto", bottom: "auto",
        width: `${box.width}px`, height: `${box.height}px`, transform: "none" });
    }
    priority();
  };
  const over = (event: PointerEvent) => { hovered = locate(event.target); priority(); };
  const out = (event: PointerEvent) => { hovered = locate(event.relatedTarget); priority(); };
  const move = (event: PointerEvent) => {
    hovered = locate(document.elementFromPoint?.(event.clientX, event.clientY) ?? event.target); priority();
  };
  const focus = (event: FocusEvent) => {
    focused = event.target instanceof Element && event.target.matches(":focus-visible") ? locate(event.target) : null; priority();
  };
  const unfocus = (event: FocusEvent) => { focused = locate(event.relatedTarget); priority(); };
  const key = () => { focused = locate(document.activeElement); priority(); };
  const pointer = () => { focused = null; priority(); };
  const blur = () => { dragged = hovered = focused = null; dragRoot = null; priority(); };
  host.addEventListener("pointerover", over); host.addEventListener("pointerout", out);
  host.addEventListener("focusin", focus); host.addEventListener("focusout", unfocus);
  window.addEventListener("keydown", key, true);
  window.addEventListener("pointerdown", pointer, true);
  window.addEventListener("pointermove", move); window.addEventListener("blur", blur); window.addEventListener("pointercancel", blur);
  window.addEventListener("pointerup", move);
  const frame = requestAnimationFrame(place);
  const clear = (root: HTMLElement) => {
    if (dragRoot === root) { dragged = null; dragRoot = null; }
    if (hovered && root.contains(hovered)) hovered = null;
    if (focused && root.contains(focused)) focused = null;
    priority();
  };
  return { place, sync, roots, clear,
    drag(root: HTMLElement, pill: HTMLElement | null) {
      if (!pill && dragRoot !== root) return;
      dragged = pill; dragRoot = pill ? root : null; priority();
    },
    dispose(root: HTMLElement) {
      clear(root);
      for (const [pill, entry] of pills) if (entry.root === root) { entry.visual.remove(); pills.delete(pill); }
      roots.delete(root);
      if (roots.size) return;
      cancelAnimationFrame(frame); slot.remove(); layers.delete(host);
      host.removeEventListener("pointerover", over); host.removeEventListener("pointerout", out);
      host.removeEventListener("focusin", focus); host.removeEventListener("focusout", unfocus);
      window.removeEventListener("keydown", key, true);
      window.removeEventListener("pointerdown", pointer, true);
      window.removeEventListener("pointermove", move); window.removeEventListener("blur", blur); window.removeEventListener("pointercancel", blur);
      window.removeEventListener("pointerup", move);
    },
  };
}

export function positionOrderPillLayer(host: HTMLElement): void { layers.get(host)?.place(); }

export function attachOrderPillLayer(host: HTMLElement, root: HTMLElement) {
  let layer = layers.get(host);
  if (!layer) { layer = createLayer(host); layers.set(host, layer); }
  const owner = layer;
  owner.roots.add(root);
  return { sync: () => owner.sync(root), drag: (pill: HTMLElement | null) => owner.drag(root, pill),
    clear: () => owner.clear(root), dispose: () => owner.dispose(root) };
}
