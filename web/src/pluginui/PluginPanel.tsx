import { useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { api, must } from "../api/client";
import { usePanelLink } from "../forge/panelLink";
import { useLibrary, withLibrary } from "../shell/library";
import { handleBridge, type Host } from "./bridge";
import { CollectionDialog } from "./CollectionDialog";
import { PushDialog } from "./PushDialog";
import { parseSSE } from "./sse";
import "./pluginui.css";

const themeTokens = ["--bg", "--surface", "--text", "--text-muted", "--accent", "--border", "--radius", "--font", "--font-size"];

async function uiCall(instance: number, method: string, params: unknown, signal: AbortSignal, onEvent: (data: unknown) => void) {
  const stream = must(
    await api.POST("/instances/{id}/ui/call", { params: { path: { id: instance } }, body: { method, params }, signal, parseAs: "stream" }),
  );
  if (!stream) throw new Error("the call ended early");
  // A holder, not the bare value: null is a real result.
  let outcome: { result: unknown } | undefined;
  await parseSSE(stream, (event, data) => {
    if (event === "event") onEvent(data);
    if (event === "result") outcome = { result: data };
    if (event === "error") throw new Error((data as { message: string }).message);
  });
  // A server shutdown can end the stream with neither a result nor an error.
  if (!outcome) throw new Error("the call ended early");
  return outcome.result;
}

/** PluginPanel shows a plugin's bundle in a sandboxed frame. Without allow-same-origin it can't read the session or call the API; messages here are its only way out. */
export function PluginPanel(props: { instance: number; title: string; src: string; grants: string[]; onClose: () => void }) {
  const { instance, title, src, grants, onClose } = props;
  const frame = useRef<HTMLIFrameElement>(null);
  const navigate = useNavigate();
  const library = useLibrary();
  const { pathname } = useLocation();
  const link = usePanelLink();
  const [dialog, setDialog] = useState<{ kind: "push" | "collection"; card: number }>();
  const running = useMemo(() => new Map<number, AbortController>(), []);
  const host = useMemo<Host>(
    () => ({
      grants,
      context: () => {
        const card = /^\/cards\/(\d+)/.exec(pathname)?.[1];
        const style = getComputedStyle(document.documentElement);
        return {
          library,
          card: card ? Number(card) : undefined,
          theme: Object.fromEntries(themeTokens.map((t) => [t, style.getPropertyValue(t)])),
          draft: link.draft(),
        };
      },
      openCard: (id) => void navigate(withLibrary(`/cards/${id}`, library)),
      // The first request stands, so a panel can't swap the card under an open dialog.
      openPush: (card) => setDialog((d) => d ?? { kind: "push", card }),
      openCollection: (card) => setDialog((d) => d ?? { kind: "collection", card }),
      call: (method, params, signal, onEvent) => uiCall(instance, method, params, signal, onEvent),
    }),
    [grants, pathname, library, navigate, instance, link],
  );
  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      const target = frame.current?.contentWindow;
      if (!target || e.source !== target) return;
      // An opaque-origin frame can only be addressed with "*"; target is the window itself.
      void handleBridge(e.data, host, running, (reply) => target.postMessage(reply, "*"));
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [host, running]);
  useEffect(() => () => running.forEach((c) => c.abort()), [running]);
  const closeDialog = () => setDialog(undefined);
  return (
    <section className="plugin-panel" aria-label={title}>
      <header>
        <strong>{title}</strong>
        <button className="button" onClick={onClose} aria-label={`Close ${title}`}>
          ×
        </button>
      </header>
      <iframe ref={frame} title={title} src={src} sandbox="allow-scripts" />
      {dialog?.kind === "push" && <PushDialog card={dialog.card} onClose={closeDialog} />}
      {dialog?.kind === "collection" && <CollectionDialog card={dialog.card} onClose={closeDialog} />}
    </section>
  );
}
