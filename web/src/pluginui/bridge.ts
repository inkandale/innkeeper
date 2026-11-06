import type { OpenDraft } from "../forge/panelLink";

/** Host is what a plugin panel may ask of the page. */
export type Host = {
  grants: string[];
  /** context's draft carries snapshot, an ID for that read, which a proposal names to start Review from it. */
  context: () => { library: number; card?: number; theme: Record<string, string>; draft?: OpenDraft & { snapshot: number } };
  openCard: (id: number) => void;
  openPush: (id: number) => void;
  openCollection: (id: number) => void;
  propose: (card: number, revision: number, snapshot: number | undefined, proposal: Record<string, string>) => void;
  call: (method: string, params: unknown, signal: AbortSignal, onEvent: (data: unknown) => void) => Promise<unknown>;
};

type Message = { id: number; call: string; method?: unknown; params?: unknown; card?: unknown; request?: unknown };

// Each request opens core's own UI; a returned string is the error to reply with.
const requests: Record<string, { grant: string; open: (h: Host, card: number, params: unknown) => string | void }> = {
  push: { grant: "cards.push", open: (h, card) => h.openPush(card) },
  "add-to-collection": { grant: "cards.collect", open: (h, card) => h.openCollection(card) },
  propose: {
    grant: "cards.edit",
    open: (h, card, params) => {
      const p = proposalOf(params);
      if (!p) return "bad proposal";
      h.propose(card, p.revision, p.snapshot, p.proposal);
    },
  },
};

// Core caps a proposal as forge-ai caps the draft it reads.
const maxProposal = 256 << 10;
// A card has far fewer text fields than this.
const maxProposalKeys = 64;

const isID = (n: unknown): n is number => typeof n === "number" && Number.isSafeInteger(n) && n > 0;

/** proposalOf is a propose request's revision, optional snapshot, and changed fields, if the first two are positive integers and the fields an object of strings within the caps. */
function proposalOf(params: unknown) {
  if (typeof params !== "object" || params === null) return;
  const { revision, snapshot, proposal } = params as { revision?: unknown; snapshot?: unknown; proposal?: unknown };
  if (!isID(revision)) return;
  if (snapshot !== undefined && !isID(snapshot)) return;
  if (typeof proposal !== "object" || proposal === null || Array.isArray(proposal)) return;
  const values = Object.values(proposal);
  if (values.length > maxProposalKeys || !values.every((v) => typeof v === "string")) return;
  if (new TextEncoder().encode(JSON.stringify(proposal)).length > maxProposal) return;
  return { revision, snapshot, proposal: { ...(proposal as Record<string, string>) } };
}

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
      const error = request.open(host, card, msg.params);
      return post(error ? { id, error } : { id, result: null });
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
