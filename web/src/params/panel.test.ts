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

describe("rendering", () => {
  const ok = (version: number) => ({ ok: true, status: 200, text: async () => JSON.stringify({ version }) });

  test("editing renders nothing until Render 3D; the change is marked unrendered", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn(async () => ok(2));
    vi.stubGlobal("fetch", fetchMock);
    const p = new ParamsPanel();
    (p as unknown as { project: string }).project = "cube";
    p.render(params);
    const button = p.el.querySelector<HTMLButtonElement>('[data-testid="render-3d"]')!;
    expect(button.disabled).toBe(true);

    const range = p.el.querySelector<HTMLInputElement>('input[type="range"]')!;
    range.value = "30";
    range.dispatchEvent(new Event("input"));
    await vi.advanceTimersByTimeAsync(5000);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(button.disabled).toBe(false);
    expect(p.el.textContent).toContain("Not rendered yet: size");

    button.click();
    await vi.advanceTimersByTimeAsync(10);
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(JSON.parse((fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1].body as string).params.size).toBe(30);
    expect(p.el.textContent).toContain("Version 2 rendered");
    expect(button.disabled).toBe(true); // rendered values are the new baseline
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  test("shows elapsed time on the button while rendering, and refuses a second press", async () => {
    vi.useFakeTimers();
    let resolve!: (v: unknown) => void;
    const fetchMock = vi.fn(() => new Promise((r) => (resolve = r)));
    vi.stubGlobal("fetch", fetchMock);
    const p = new ParamsPanel();
    (p as unknown as { project: string }).project = "cube";
    p.render(params);
    const text = p.el.querySelector<HTMLInputElement>('input[type="text"]')!;
    text.value = "B2";
    text.dispatchEvent(new Event("input"));
    text.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    await vi.advanceTimersByTimeAsync(3000);
    const button = p.el.querySelector<HTMLButtonElement>('[data-testid="render-3d"]')!;
    expect(button.textContent).toContain("Rendering… 3 s");
    expect(button.disabled).toBe(true);
    button.click();
    resolve(ok(2));
    await vi.advanceTimersByTimeAsync(10);
    expect(fetchMock).toHaveBeenCalledOnce();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  test("Reset puts back the shown version's values", () => {
    const p = new ParamsPanel();
    p.render(params);
    const box = p.el.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    box.checked = false;
    box.dispatchEvent(new Event("change"));
    expect(p.edits()).toEqual({ chamfer: false });
    p.el.querySelector<HTMLButtonElement>('button:not([data-testid])')!.click();
    expect(p.edits()).toEqual({});
  });

  test("unrendered edits survive a reload, e.g. when the agent renders meanwhile", () => {
    const p = new ParamsPanel();
    p.render(params, { size: 42 });
    expect(p.edits()).toEqual({ size: 42 });
    expect(p.values().size).toBe(42);
  });
});
