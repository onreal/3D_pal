import type { Approval, Estimate, Job, PrinterStatus, Vec } from "./api";

export interface EventMap {
  model: { project: string; version: number; size_mm: Vec; fits: boolean };
  job: Partial<Approval> & { job: Job };
  estimate: Estimate;
  printer: { printer_id: string; status: PrinterStatus; job?: Job };
  settings: { saved_at: string };
}

type Handler<K extends keyof EventMap> = (data: EventMap[K]) => void;

/** Live updates from the server over Server-Sent Events; the browser reconnects on its own. */
export class Live {
  private source: EventSource | null = null;
  private handlers: { [K in keyof EventMap]?: Handler<K>[] } = {};
  onConnection: (connected: boolean) => void = () => {};

  connect(url = "/api/events") {
    this.source = new EventSource(url);
    this.source.onopen = () => this.onConnection(true);
    this.source.onerror = () => this.onConnection(false);
    for (const type of ["model", "job", "estimate", "printer", "settings"] as const) {
      this.source.addEventListener(type, (e) => {
        let data: unknown;
        try {
          data = JSON.parse((e as MessageEvent<string>).data);
        } catch {
          return;
        }
        for (const fn of this.handlers[type] ?? []) (fn as (d: unknown) => void)(data);
      });
    }
  }

  on<K extends keyof EventMap>(type: K, fn: Handler<K>) {
    ((this.handlers[type] ??= []) as Handler<K>[]).push(fn);
  }

  close() {
    this.source?.close();
  }
}
