import { describe, expect, test, vi } from "vitest";
import type { Param } from "../api";
import { ParamsPanel, control } from "./panel";

const params: Param[] = [
  { name: "size", type: "number", value: 20, min: 5, max: 100, step: 1, label: "Edge length" },
  { name: "chamfer", type: "boolean", value: true },
  { name: "fit", type: "number", value: 0.2, options: [{ value: 0.1, label: "Tight" }, { value: 0.2, label: "Normal" }] },
  { name: "label", type: "string", value: "A1", group: "Text" },
  { name: "offset", type: "vector", value: [1, 2] },
];

describe("parameter controls", () => {
  test("a ranged number gets a slider kept in step with its field", () => {
    const onInput = vi.fn();
    const c = control(params[0]!, onInput);
    const range = c.el.querySelector<HTMLInputElement>('input[type="range"]')!;
    expect(range.min).toBe("5");
    expect(range.max).toBe("100");
    range.value = "42";
    range.dispatchEvent(new Event("input"));
    expect(c.read()).toBe(42);
    expect(onInput).toHaveBeenCalledOnce();
  });

  test("options become a dropdown with the typed value", () => {
    const c = control(params[2]!, () => {});
    const select = c.el.querySelector("select")!;
    expect(select.value).toBe("0.2");
    select.value = "0.1";
    expect(c.read()).toBe(0.1);
  });

  test("the panel reads every value back in its type, grouped", () => {
    const p = new ParamsPanel();
    p.render(params);
    expect(p.values()).toEqual({ size: 20, chamfer: true, fit: 0.2, label: "A1", offset: [1, 2] });
    expect(p.el.querySelector("legend")?.textContent).toBe("Text");
  });

  test("a model without parameters explains how to add them", () => {
    const p = new ParamsPanel();
    p.render([]);
    expect(p.el.textContent).toContain("no top-level parameters");
  });
});
