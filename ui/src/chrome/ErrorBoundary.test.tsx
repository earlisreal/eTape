// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ErrorBoundary } from "./ErrorBoundary";

function Boom(): JSX.Element { throw new Error("panel exploded"); }

describe("ErrorBoundary", () => {
  it("renders an inline error card when a child throws", () => {
    render(<ErrorBoundary label="Chart"><Boom /></ErrorBoundary>);
    expect(screen.getByText(/Chart/)).toBeTruthy();
    expect(screen.getByRole("button", { name: /reload/i })).toBeTruthy();
  });
});
