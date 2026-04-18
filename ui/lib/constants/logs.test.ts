import { describe, expect, it } from "vitest";

import { RequestTypeColors, RequestTypeLabels, RequestTypes } from "./logs";

describe("logs constants", () => {
	it("registers search as a known request type", () => {
		expect(RequestTypes).toContain("search");
		expect(RequestTypeLabels.search).toBe("Search");
		expect(RequestTypeColors.search).toBeTruthy();
	});

	it("registers realtime turn as a known request type", () => {
		expect(RequestTypes).toContain("realtime.turn");
		expect(RequestTypeLabels["realtime.turn"]).toBe("Realtime Turn");
		expect(RequestTypeColors["realtime.turn"]).toBeTruthy();
	});
});
