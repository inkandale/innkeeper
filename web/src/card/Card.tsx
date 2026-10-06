import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useBlocker, useParams } from "react-router";
import { api, ApiError, must } from "../api/client";
import type { components } from "../api/schema";
import { useToast } from "../shell/Toasts";
import { changed, rebase, type Data, type Draft } from "./draft";
import { Conflict } from "./Conflict";
import { Fields } from "./Fields";
import { Panel } from "./Panel";
import "./Card.css";

export type Entity = components["schemas"]["EntityOut"];

/** Card is the card page. It's keyed by ID so another card starts with no draft. */
export function Card() {
  const { id } = useParams();
  return <Editor key={id} id={Number(id)} />;
}

function Editor({ id }: { id: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const card = useQuery({
    queryKey: ["card", id],
    queryFn: async () => must(await api.GET("/cards/{id}", { params: { path: { id } } })),
  });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [stale, setStale] = useState(false);
  const [comparing, setComparing] = useState(false);
  const dirty = changed(draft);
  useLeaveGuard(dirty.size > 0);

  const save = useMutation({
    mutationFn: async (d: Draft) =>
      must(await api.PUT("/cards/{id}", { params: { path: { id } }, body: { base_revision_id: d.baseId, data: d.data, source: "ui" } })),
    onSuccess: (e, sent) => {
      qc.setQueryData(["card", id], e);
      void qc.invalidateQueries({ queryKey: ["card", id, "revisions"] });
      // Keep edits typed while the save was in flight, on top of the new head.
      setDraft((cur) => {
        if (!cur || cur === sent) return null;
        const next = rebase(cur, e.revision.id, (e.revision.data ?? {}) as Data);
        return changed(next).size ? next : null;
      });
      setStale(false);
      setComparing(false);
      toast({ text: "Saved" });
    },
    onError: (e) => (e instanceof ApiError && e.status === 409 ? setStale(true) : toast({ text: e.message, error: true })),
  });

  // An image change saves at once, on the head. A draft started from that
  // head moves onto the new revision, since its data didn't change.
  const avatar = useMutation({
    mutationFn: async ({ sha, base }: { sha: string; base: Entity["revision"] }) =>
      must(
        await api.PUT("/cards/{id}", {
          params: { path: { id } },
          body: { base_revision_id: base.id, data: base.data, avatar: sha, message: "Replace image", source: "ui" },
        }),
      ),
    onSuccess: (e, { base }) => {
      qc.setQueryData(["card", id], e);
      void qc.invalidateQueries({ queryKey: ["card", id, "revisions"] });
      setDraft((d) => (d && d.baseId === base.id ? { ...d, baseId: e.revision.id } : d));
    },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        // The head moved on, so the button's base is stale; refetch it.
        void qc.invalidateQueries({ queryKey: ["card", id] });
        toast({ text: "This card changed elsewhere. Try again.", error: true });
      } else toast({ text: e.message, error: true });
    },
  });

  if (card.isPending) return null;
  if (card.isError) return <p className="error page">{card.error.message}</p>;
  const head = card.data;
  const headData = (head.revision.data ?? {}) as Data;
  const data = draft?.data ?? headData;
  const edit = (key: string, value: unknown) =>
    setDraft((cur) => {
      const d = cur ?? { baseId: head.revision.id, base: headData, data: headData };
      return { ...d, data: { ...d.data, [key]: value } };
    });
  const reload = () => {
    setDraft(null);
    setStale(false);
    setComparing(false);
    void card.refetch();
  };
  const compare = async () => {
    await card.refetch();
    setComparing(true);
  };
  // Keep moves the draft onto the head that beat it, so Save can go ahead.
  const keep = () => {
    if (draft) setDraft(rebase(draft, head.revision.id, headData));
    setStale(false);
    setComparing(false);
  };
  const use = (key: string) => setDraft((cur) => cur && { ...cur, data: { ...cur.data, [key]: headData[key] } });

  return (
    <div className="card-page">
      <Panel card={head} data={data} edit={edit} dirty={dirty.size > 0} saving={save.isPending || stale || avatar.isPending} onSave={() => draft && save.mutate(draft)}
        onAvatar={(sha) => avatar.mutate({ sha, base: head.revision })}
        avatarBusy={avatar.isPending || save.isPending}
      />
      <div className="card-main">
        {stale && (
          <div className="banner" role="alert">
            <span>This card changed elsewhere since you started editing.</span>
            <button className="button" onClick={reload}>
              Reload and lose my edits
            </button>
            <button className="button" onClick={() => void compare()}>
              Compare with the latest
            </button>
          </div>
        )}
        {stale && comparing && draft && <Conflict latest={headData} draft={draft} onUse={use} onKeep={keep} />}
        <Fields data={data} dirty={dirty} edit={edit} />
      </div>
    </div>
  );
}

// useLeaveGuard asks before leaving the page while on is true: in the app
// through the router, and on reload or close through the browser.
function useLeaveGuard(on: boolean) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => on && currentLocation.pathname !== nextLocation.pathname);
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    if (window.confirm("Leave without saving your edits?")) blocker.proceed();
    else blocker.reset();
  }, [blocker]);
  useEffect(() => {
    if (!on) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [on]);
}
