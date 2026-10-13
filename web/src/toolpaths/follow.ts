import type { PrinterStatus } from "../api";

/**
 * Following a print: the printer reports its layer (exact) but not where the nozzle is in
 * that layer. The position inside the layer is estimated from how long the previous layer
 * took, and snaps back to the truth on every new layer.
 */
export class LayerClock {
  private layer = 0;
  private startedAt = 0; // when the current layer was first seen
  private lastDuration = 0; // how long the previous layer took, if we saw it whole

  /** Records a status; returns true when the layer changed. */
  observe(layer: number, now: number): boolean {
    if (layer === this.layer) return false;
    // Only a step to the next layer measures one; a jump (first sight, a restart) does not.
    this.lastDuration = layer === this.layer + 1 && this.startedAt ? now - this.startedAt : 0;
    this.layer = layer;
    this.startedAt = now;
    return true;
  }

  /** Estimated share of the current layer done, 0 to 0.97; null when there's nothing to go on yet. */
  fraction(now: number): number | null {
    if (!this.lastDuration) return null;
    return Math.max(0, Math.min(0.97, (now - this.startedAt) / this.lastDuration));
  }

  reset() {
    this.layer = this.startedAt = this.lastDuration = 0;
  }
}

export const isActive = (s: PrinterStatus | null | undefined) => s?.state === "printing" || s?.state === "paused";

/** "Printing · layer 37 / 100 · 42% · about 1 h 05 min left". */
export function liveLabel(s: PrinterStatus): string {
  const state = { printing: "Printing", paused: "Paused", finished: "Finished", failed: "Failed", idle: "Idle", offline: "Offline" }[s.state];
  const parts = [state];
  if (s.total_layers) parts.push(`layer ${Math.min(s.layer, s.total_layers)} / ${s.total_layers}`);
  if (isActive(s)) {
    parts.push(`${Math.round(s.progress * 100)}%`);
    if (s.remaining_s) parts.push(`about ${duration(s.remaining_s)} left`);
  }
  return parts.join(" · ");
}

export function duration(seconds: number): string {
  const m = Math.round(seconds / 60);
  if (m < 60) return `${m} min`;
  return `${Math.floor(m / 60)} h ${String(m % 60).padStart(2, "0")} min`;
}

/** The layers to draw while following: everything up to the current layer (1-based), all when done. */
export function followRange(s: PrinterStatus, count: number): [number, number] {
  if (s.state === "finished" || !s.layer) return [0, Math.max(count - 1, 0)];
  return [0, Math.max(0, Math.min(s.layer, count) - 1)];
}
