import type { Approval, AssemblyResult, Estimate, Job, PrinterStatus, SessionInfo, Vec } from "./api";

/** The agent session that caused an event; absent for the viewer's own actions. */
export interface FromSession { session?: string; client?: string }

export interface EventMap {
  model: FromSession & { project: string; version: number; size_mm: Vec; fits: boolean };
  job: FromSession & Partial<Approval> & { job: Job };
  estimate: FromSession & Estimate;
  printer: FromSession & { printer_id: string; status: PrinterStatus; job?: Job };
  settings: { saved_at: string };
  session: { kind: "opened" | "updated" | "closed"; session: SessionInfo };
  assembly: AssemblyResult;
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
    for (const type of ["model", "job", "estimate", "printer", "settings", "session", "assembly"] as const) {
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
