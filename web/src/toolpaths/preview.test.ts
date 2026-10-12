import { strToU8, zipSync } from "fflate";
import { expect, test } from "vitest";
import { clampLayer, plateGcode } from "./preview";

test("reads the plate G-code out of a .gcode.3mf", async () => {
  const zip = zipSync({
    "Metadata/slice_info.config": strToU8("<config/>"),
    "Metadata/plate_1.gcode": strToU8("; HEADER_BLOCK_START\nG28\n"),
  });
  const buf = zip.buffer.slice(zip.byteOffset, zip.byteOffset + zip.byteLength) as ArrayBuffer;
  expect(await plateGcode(buf)).toContain("G28");
});

test("a file without plate G-code is an error", async () => {
  const zip = zipSync({ "x.txt": strToU8("x") });
  await expect(plateGcode(zip.buffer as ArrayBuffer)).rejects.toThrow("no plate G-code");
});

test("layer slider clamps to the parsed range", () => {
  expect(clampLayer(0, 100)).toBe(1);
  expect(clampLayer(250, 100)).toBe(100);
  expect(clampLayer(42.4, 100)).toBe(42);
});
