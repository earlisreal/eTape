// @vitest-environment jsdom
import { StrictMode, useRef, useState } from "react";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Dialog } from "./Dialog";
import { useDismissibleLayer } from "./useDismissibleLayer";
import { modalTracker } from "../modalTracker";

afterEach(cleanup);

describe("Dialog", () => {
  it("dismisses on backdrop and Escape, but keeps inside clicks and a drag ending outside", () => {
    const close = vi.fn();
    render(<Dialog title="Example" onClose={close}><button>Inside</button></Dialog>);
    const dialog = screen.getByRole("dialog", { name: "Example" });
    const scrim = dialog.parentElement!;
    fireEvent.click(screen.getByRole("button", { name: "Inside" }));
    fireEvent.mouseDown(dialog);
    fireEvent.click(scrim);
    expect(close).not.toHaveBeenCalled();
    fireEvent.mouseDown(scrim);
    fireEvent.click(scrim);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(close).toHaveBeenCalledTimes(2);
  });

  it("contains keyboard focus and restores the opener after dismissal", () => {
    function Example() {
      const [open, setOpen] = useState(false);
      return <><button onClick={() => setOpen(true)}>Open</button>{open && <Dialog title="Example" onClose={() => setOpen(false)}><button>Last</button></Dialog>}</>;
    }
    render(<StrictMode><Example /></StrictMode>);
    const opener = screen.getByRole("button", { name: "Open" });
    opener.focus(); fireEvent.click(opener);
    const first = screen.getByRole("button", { name: "Close dialog" });
    const last = screen.getByRole("button", { name: "Last" });
    expect(document.activeElement).toBe(first);
    fireEvent.keyDown(first, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last);
    fireEvent.keyDown(last, { key: "Tab" });
    expect(document.activeElement).toBe(first);
    fireEvent.keyDown(first, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(opener);
    expect(modalTracker.isOpen()).toBe(false);
  });

  it("dismisses a nested dialog before its parent and restores the parent's focus", () => {
    function Example() {
      const [child, setChild] = useState(false);
      return <Dialog title="Parent" onClose={vi.fn()}><button onClick={() => setChild(true)}>Child</button>
        {child && <Dialog title="Child" onClose={() => setChild(false)}>Nested</Dialog>}
      </Dialog>;
    }
    render(<Example />);
    const opener = screen.getByRole("button", { name: "Child" });
    opener.focus(); fireEvent.click(opener);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "Child" })).toBeNull();
    expect(screen.getByRole("dialog", { name: "Parent" })).toBeTruthy();
    expect(document.activeElement).toBe(opener);
    expect(modalTracker.isOpen()).toBe(true);
  });

  it("blocks dismissal while an operation is pending", () => {
    const close = vi.fn();
    render(<Dialog title="Saving" onClose={close} dismissible={false}>Pending</Dialog>);
    fireEvent.click(screen.getByRole("dialog").parentElement!);
    fireEvent.keyDown(window, { key: "Escape" });
    expect((screen.getByRole("button", { name: "Close dialog" }) as HTMLButtonElement).disabled).toBe(true);
    expect(close).not.toHaveBeenCalled();
  });

  it("dismisses a popup on outside input and consumes only its own Escape", () => {
    const parentClose = vi.fn(); const menuClose = vi.fn();
    function Popup() {
      const ref = useRef<HTMLDivElement>(null);
      useDismissibleLayer(ref, menuClose);
      return <div ref={ref} role="menu"><button>Choice</button></div>;
    }
    function Example() {
      const [menu, setMenu] = useState(false);
      return <Dialog title="Parent" onClose={parentClose}><button onClick={() => setMenu(true)}>Menu</button>{menu && <Popup />}</Dialog>;
    }
    render(<Example />);
    fireEvent.click(screen.getByRole("button", { name: "Menu" }));
    fireEvent.mouseDown(within(screen.getByRole("menu")).getByRole("button"));
    expect(menuClose).not.toHaveBeenCalled();
    fireEvent.mouseDown(document.body);
    fireEvent.touchStart(document.body);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(menuClose).toHaveBeenCalledTimes(3);
    expect(parentClose).not.toHaveBeenCalled();
  });
});
