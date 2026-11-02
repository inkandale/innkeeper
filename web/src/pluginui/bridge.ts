import type { OpenDraft } from "../forge/panelLink";

/** Host is what a plugin panel may ask of the page. */
export type Host = {
  grants: string[];
  context: () => { library: number; card?: number; theme: Record<string, string>; draft?: OpenDraft };
  openCard: (id: number) => void;
  openPush: (id: number) => void;
  openCollection: (id: number) => void;
  call: (method: string, params: unknown, signal: AbortSignal, onEvent: (data: unknown) => void) => Promise<unknown>;
};

type Message = { id: number; call: string; method?: unknown; params?: unknown; card?: unknown; request?: unknown };

const requests: Record<string, { grant: string; open: (h: Host, id: number) => void }> = {
  push: { grant: "cards.push", open: (h, id) => h.openPush(id) },
  "add-to-collection": { grant: "cards.collect", open: (h, id) => h.openCollection(id) },
};

// Each running call holds a server stream, and a panel is untrusted.
const maxRunning = 4;

/** handleBridge answers one panel message. Panel code is untrusted, so every field is checked here. */
export async function handleBridge(raw: unknown, host: Host, running: Map<number, AbortController>, post: (reply: unknown) => void) {
  if (typeof raw !== "object" || raw === null) return;
  const msg = raw as Message;
  if (!Number.isSafeInteger(msg.id) || typeof msg.call !== "string") return;
  const { id } = msg;
  const card = typeof msg.card === "number" && Number.isSafeInteger(msg.card) && msg.card > 0 ? msg.card : undefined;
  switch (msg.call) {
    case "host.context": {
      const { draft, ...context } = host.context();
      // The open card's draft goes only to a panel the owner let edit cards.
      const shared = draft && host.grants.includes("cards.edit") ? { draft } : {};
      return post({ id, result: { ...context, ...shared, grants: host.grants } });
    }
    case "host.openCard":
      if (card === undefined) return post({ id, error: "bad card" });
      host.openCard(card);
      return post({ id, result: null });
    case "host.request": {
      const request = typeof msg.request === "string" && Object.hasOwn(requests, msg.request) ? requests[msg.request] : undefined;
      if (!request) return post({ id, error: "unknown request" });
      if (card === undefined) return post({ id, error: "bad card" });
      if (!host.grants.includes(request.grant)) return post({ id, error: "not granted" });
      request.open(host, card);
      return post({ id, result: null });
    }
    case "ui.abort":
      running.get(id)?.abort();
      return;
    case "ui.call": {
      if (typeof msg.method !== "string") return post({ id, error: "bad method" });
      if (running.has(id)) return post({ id, error: "id in use" });
      if (running.size >= maxRunning) return post({ id, error: "too many calls" });
      const controller = new AbortController();
      running.set(id, controller);
      try {
        const result = await host.call(msg.method, msg.params, controller.signal, (event) => post({ id, event }));
        post({ id, result });
      } catch (e) {
        post({ id, error: e instanceof Error ? e.message : "the call failed" });
      } finally {
        running.delete(id);
      }
      return;
    }
    default:
      post({ id, error: "unknown call" });
  }
}
