// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { AboutSection } from "./AboutSection";
import { AppProviders } from "../test/providers";

describe("AboutSection", () => {
  it("displays the running engine version", async () => {
    const sendQuery = vi.fn().mockResolvedValue({ version: "v1.2.3" });
    render(<AppProviders><AboutSection commands={{ sendQuery }} /></AppProviders>);

    await waitFor(() => expect(screen.getByText("v1.2.3")).toBeTruthy());
    expect(sendQuery).toHaveBeenCalledWith("QueryAppInfo", {});
  });
});
