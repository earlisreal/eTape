import { describe, it, expectTypeOf } from "vitest";
import type { Order, Quote, Bar, ExecStatus, SubmitOrderArgs, TopicName, VenueID, ScannerSession } from "./contract";
import type * as Gen from "../gen/wsmsg";

describe("contract re-exports the generated wire types", () => {
  it("payload types are the generated types", () => {
    expectTypeOf<Order>().toEqualTypeOf<Gen.Order>();
    expectTypeOf<Quote>().toEqualTypeOf<Gen.Quote>();
    expectTypeOf<Bar>().toEqualTypeOf<Gen.Bar>();
    expectTypeOf<ExecStatus>().toEqualTypeOf<Gen.ExecStatus>();
    expectTypeOf<SubmitOrderArgs>().toEqualTypeOf<Gen.SubmitOrderArgs>();
  });
  it("TopicName aliases the generated Topic union", () => {
    expectTypeOf<TopicName>().toEqualTypeOf<Gen.Topic>();
  });
  it("VenueID + ScannerSession stay UI-side string types", () => {
    expectTypeOf<VenueID>().toEqualTypeOf<string>();
    expectTypeOf<ScannerSession>().toEqualTypeOf<"premarket" | "rth" | "afterhours">();
  });
});
