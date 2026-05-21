import { describe, expect, it } from "vitest";

import {
  EdgeErrorCode,
  type EdgeErrorCode as EdgeErrorCodeValue,
} from "./generated/model/edgeErrorCode";

describe("generated EdgeErrorCode", () => {
  it("includes shadow step-up and limit codes for type narrowing", () => {
    const codes: EdgeErrorCodeValue[] = [
      EdgeErrorCode.step_up_required,
      EdgeErrorCode.limit_exceeded,
    ];
    const handlesShadowCodes = (code: EdgeErrorCodeValue): boolean => {
      switch (code) {
        case EdgeErrorCode.step_up_required:
        case EdgeErrorCode.limit_exceeded:
          return true;
        default:
          return false;
      }
    };

    expect(codes).toEqual(["step_up_required", "limit_exceeded"]);
    expect(codes.every(handlesShadowCodes)).toBe(true);
  });
});
