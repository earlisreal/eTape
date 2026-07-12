// @vitest-environment jsdom
import { createRef } from "react";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { Button } from "./Button";

const here = dirname(fileURLToPath(import.meta.url));

describe("Button", () => {
  it("defaults to variant=neutral, size=sm, type=button", () => {
    render(<Button>Click</Button>);
    const btn = screen.getByRole("button", { name: "Click" }) as HTMLButtonElement;
    expect(btn.className).toBe("btn");
    expect(btn.type).toBe("button");
  });

  it("renders a distinct class per variant", () => {
    render(
      <>
        <Button variant="primary">Primary</Button>
        <Button variant="neutral">Neutral</Button>
        <Button variant="danger" confirm>Danger</Button>
        <Button variant="quiet">Quiet</Button>
      </>,
    );
    expect((screen.getByText("Primary") as HTMLButtonElement).className).toBe("btn btn-primary");
    expect((screen.getByText("Neutral") as HTMLButtonElement).className).toBe("btn");
    expect((screen.getByText("Danger") as HTMLButtonElement).className).toBe("btn btn-danger");
    expect((screen.getByText("Quiet") as HTMLButtonElement).className).toBe("btn btn-quiet");
  });

  it("size=md adds btn-md; size=sm (default) does not", () => {
    render(
      <>
        <Button size="md">Modal CTA</Button>
        <Button size="sm">Compact</Button>
      </>,
    );
    expect((screen.getByText("Modal CTA") as HTMLButtonElement).className).toBe("btn btn-md");
    expect((screen.getByText("Compact") as HTMLButtonElement).className).toBe("btn");
  });

  it("iconOnly adds btn-icon", () => {
    render(<Button iconOnly aria-label="close">×</Button>);
    expect(screen.getByLabelText("close").className).toBe("btn btn-icon");
  });

  it("loading adds btn-loading and forces disabled, even without an explicit disabled prop", () => {
    render(<Button loading>Save</Button>);
    const btn = screen.getByRole("button", { name: "Save" }) as HTMLButtonElement;
    expect(btn.className).toBe("btn btn-loading");
    expect(btn.disabled).toBe(true);
  });

  it("forwards a ref to the underlying button DOM node", () => {
    const ref = createRef<HTMLButtonElement>();
    render(<Button ref={ref}>ref btn</Button>);
    expect(ref.current).toBeInstanceOf(HTMLButtonElement);
    expect(ref.current?.textContent).toBe("ref btn");
  });

  it("passes through standard props (data-testid, aria-label, disabled) and fires onClick without confirm", () => {
    const onClick = vi.fn();
    render(<Button data-testid="my-btn" aria-label="do it" onClick={onClick}>Go</Button>);
    const btn = screen.getByTestId("my-btn");
    fireEvent.click(btn);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  describe("confirm state machine", () => {
    beforeEach(() => { vi.useFakeTimers(); });
    afterEach(() => { vi.useRealTimers(); });

    it("arms on first click (shows confirmLabel, does not fire onClick), fires once on second click and disarms", () => {
      const onClick = vi.fn();
      render(<Button variant="danger" confirm confirmLabel="Confirm remove" onClick={onClick}>Remove</Button>);
      const btn = screen.getByRole("button") as HTMLButtonElement;
      expect(btn.textContent).toBe("Remove");

      fireEvent.click(btn);
      expect(btn.textContent).toBe("Confirm remove");
      expect(onClick).not.toHaveBeenCalled();

      fireEvent.click(btn);
      expect(onClick).toHaveBeenCalledTimes(1);
      expect(btn.textContent).toBe("Remove"); // disarmed back to the resting label
    });

    it("defaults the armed label to \"Sure?\" when confirmLabel is omitted", () => {
      render(<Button confirm>Do it</Button>);
      const btn = screen.getByRole("button") as HTMLButtonElement;
      fireEvent.click(btn);
      expect(btn.textContent).toBe("Sure?");
    });

    it("reverts to the resting label after ~3s without a second click, and a subsequent click re-arms instead of firing", () => {
      const onClick = vi.fn();
      render(<Button confirm confirmLabel="Confirm reset" onClick={onClick}>Reset</Button>);
      const btn = screen.getByRole("button") as HTMLButtonElement;

      fireEvent.click(btn);
      expect(btn.textContent).toBe("Confirm reset");

      act(() => { vi.advanceTimersByTime(3000); });
      expect(btn.textContent).toBe("Reset");

      // The revert means this click re-arms rather than firing.
      fireEvent.click(btn);
      expect(onClick).not.toHaveBeenCalled();
      expect(btn.textContent).toBe("Confirm reset");
    });

    it("without confirm, ignores confirmLabel and fires onClick on every click", () => {
      const onClick = vi.fn();
      render(<Button confirmLabel="Confirm reset" onClick={onClick}>Reset</Button>);
      const btn = screen.getByRole("button") as HTMLButtonElement;
      fireEvent.click(btn);
      expect(btn.textContent).toBe("Reset");
      expect(onClick).toHaveBeenCalledTimes(1);
    });
  });

  describe("global.css", () => {
    const css = readFileSync(join(here, "..", "..", "global.css"), "utf8");

    it("gives every .btn a visible bronze :focus-visible ring", () => {
      expect(css).toMatch(/\.btn:focus-visible\s*\{[^}]*outline:\s*2px solid var\(--accent\)/);
    });

    it("honors prefers-reduced-motion by zeroing the --btn-transition custom property Button.tsx reads inline", () => {
      expect(css).toMatch(/@media \(prefers-reduced-motion:\s*reduce\)\s*\{\s*:root\s*\{\s*--btn-transition:\s*none;/);
    });
  });

  describe("danger variant requires confirm (grep-style)", () => {
    // A cheap static guard so a future AI-authored change can't casually
    // recolor a benign button red without the two-click gate that makes
    // danger mean something (spec §D). Excludes *.test.tsx (this file's own
    // unit tests above deliberately render variant="danger" without confirm
    // to exercise plain click-through behavior) and TVDialog's chart-dialog
    // buttons, which are a separate system by design and never use Button.
    function collectTsxFiles(dir: string): string[] {
      const out: string[] = [];
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        if (entry.name === "node_modules") continue;
        const full = join(dir, entry.name);
        if (entry.isDirectory()) { out.push(...collectTsxFiles(full)); continue; }
        if (entry.name.endsWith(".tsx") && !entry.name.endsWith(".test.tsx")) out.push(full);
      }
      return out;
    }

    it("every variant=\"danger\" usage under src/ also passes confirm, or carries a confirm-external comment, on the same line", () => {
      const srcRoot = join(here, "..", "..");
      const offenders: string[] = [];
      for (const file of collectTsxFiles(srcRoot)) {
        const lines = readFileSync(file, "utf8").split("\n");
        lines.forEach((line, i) => {
          if (!line.includes('variant="danger"')) return;
          const hasConfirm = /\bconfirm\b/.test(line);
          const hasExemption = line.includes("confirm-external:");
          if (!hasConfirm && !hasExemption) offenders.push(`${file}:${i + 1}: ${line.trim()}`);
        });
      }
      expect(offenders).toEqual([]);
    });
  });
});
