import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { api, must, upload } from "../api/client";
import type { components } from "../api/schema";
import type { Data } from "../card/draft";
import { finished, type Job } from "../settings/jobUtil";
import { changedKeys, overlaps, readSettled, scopeOf, settle as remember, lockOf, type Action, type Payload, type Progress, type Result, type Scope } from "./forge";

type ContextRefs = components["schemas"]["ContextRefs"];

/** RunRequest is a run the owner asked for. An image is a blob SHA, or a file to upload when the run starts. */
export type RunRequest = {
  action: Action;
  field?: string;
  instruction?: string;
  inputs?: unknown;
  context?: ContextRefs;
  images?: { reference?: Blob | string; avatar?: Blob | string };
};

/** Run is an action job on this card, as the Forge shows it. */
export type Run = {
  id: number;
  job: Job;
  label: string;
  field?: string;
  scope: Scope;
  instance: string;
  /** start is the draft the run began from. */
  start: Data;
  live: boolean;
  result?: Result;
};

/** Waiting is a run held in this tab until the runs its scope overlaps finish. */
export type Waiting = { key: number; req: RunRequest; label: string; field?: string; scope: Scope; instance: string };

const instanceKey = "innkeeper.forge.instance";

function readInstance(): number | undefined {
  try {
    const v = Number(localStorage.getItem(instanceKey));
    return v || undefined;
  } catch {
    return undefined;
  }
}

const jobQuery = (id: number) => ({
  queryKey: ["job", id],
  queryFn: async () => must(await api.GET("/jobs/{id}", { params: { path: { id } } })),
  // A finished job never changes.
  staleTime: (q: { state: { data?: Job } }) => (q.state.data && finished(q.state.data) ? Infinity : 0),
});

async function sha(image: Blob | string | undefined) {
  return typeof image === "string" || image === undefined ? image : upload(image);
}

let waitingKey = 0;

/**
 * useForge holds a card's Forge: the actions on offer, the chosen model setup (an action instance), and the card's
 * runs, found from the job list so they survive a reload. data is the draft a run starts from. cardId is undefined
 * for a new card, whose runs live only in this tab.
 */
export function useForge({ cardId, data }: { cardId?: number; data: Data }) {
  const qc = useQueryClient();
  const actionList = useQuery({ queryKey: ["actions"], queryFn: async () => must(await api.GET("/actions")) });
  const actions = actionList.data ?? [];
  const instances = [...new Map(actions.map((a) => [a.instance_id, { id: a.instance_id, name: a.instance_name }])).values()];
  const [chosen, setChosen] = useState(readInstance);
  const instance = instances.find((i) => i.id === chosen) ?? instances[0];
  const setInstance = (id: number) => {
    setChosen(id);
    try {
      localStorage.setItem(instanceKey, String(id));
    } catch {
      // Remembered for this page only.
    }
  };
  const declOf = (job: Job) => actions.find((a) => a.instance_id === job.instance_id && a.id === (job.payload as Payload).action);
  const nameOf = (id: number | undefined) => instances.find((i) => i.id === id)?.name ?? `instance ${id}`;

  const list = useQuery({
    queryKey: ["jobs", "action", cardId],
    enabled: cardId !== undefined,
    queryFn: async () => must(await api.GET("/jobs", { params: { query: { kind: "action", card_id: cardId, limit: 20 } } })),
  });
  const [started, setStarted] = useState<number[]>([]);
  const [settled, setSettled] = useState(readSettled);
  const summaries = new Map((list.data ?? []).map((j) => [j.id, j]));
  const ids = [...new Set([...summaries.keys(), ...started])].sort((a, b) => a - b);
  // The newest report survives settling its run, so the Report tab keeps it.
  const reportId = (list.data ?? []).find((j) => j.status === "succeeded" && declOf(j)?.report_only)?.id;
  const detailIds = ids.filter((id) => !settled.has(id) || id === reportId);
  const details = useQueries({ queries: detailIds.map(jobQuery) });
  const jobOf = (id: number) => details[detailIds.indexOf(id)]?.data ?? summaries.get(id);

  const runs: Run[] = ids.flatMap((id) => {
    const job = jobOf(id);
    if (!job) return [];
    const p = job.payload as Payload;
    const decl = declOf(job);
    return [
      {
        id,
        job,
        label: decl?.label ?? p.action,
        field: p.field,
        scope: scopeOf(decl, p.field),
        instance: nameOf(job.instance_id),
        start: p.entity.data ?? {},
        live: !finished(job),
        result: (job.progress as Progress | null)?.result,
      },
    ];
  });
  const shown = runs.filter((r) => r.live || !settled.has(r.id));
  const [waiting, setWaiting] = useState<Waiting[]>([]);
  const busy = [...runs.filter((r) => r.live), ...waiting];
  const lock = lockOf(busy);
  const pending = shown.find((r) => r.job.status === "succeeded" && r.result?.proposal && changedKeys(r.start, r.result.proposal).length > 0);
  const report = [...runs].reverse().find((r) => r.result?.report)?.result?.report ?? undefined;

  // A run starts from the draft as it is when it starts, which for a waiting run is later than when it was asked for.
  const dataRef = useRef(data);
  dataRef.current = data;
  const post = useMutation({
    mutationFn: async ({ action, field, instruction, inputs, context, images }: RunRequest) => {
      const [reference, avatar] = await Promise.all([sha(images?.reference), sha(images?.avatar)]);
      return must(
        await api.POST("/actions/runs", {
          body: {
            instance_id: action.instance_id,
            action: action.id,
            field,
            entity: { kind: "card", id: cardId, data: action.applies === "new" ? undefined : dataRef.current },
            instruction,
            inputs: inputs as Record<string, unknown> | undefined,
            context,
            images: { reference, avatar },
          },
        }),
      );
    },
    onSuccess: (job) => {
      qc.setQueryData(["job", job.id], job);
      setStarted((s) => [...s, job.id]);
      void qc.invalidateQueries({ queryKey: ["jobs", "action", cardId] });
    },
  });

  const start = (req: RunRequest) => {
    const w: Waiting = { key: ++waitingKey, req, label: req.action.label, field: req.field, scope: scopeOf(req.action, req.field), instance: req.action.instance_name };
    if (post.isPending || busy.some((b) => overlaps(b.scope, w.scope))) setWaiting((list) => [...list, w]);
    else post.mutate(req);
  };
  const liveScopes = runs.filter((r) => r.live).map((r) => r.scope);
  useEffect(() => {
    if (post.isPending) return;
    const next = waiting.find((w) => !liveScopes.some((s) => overlaps(s, w.scope)));
    if (!next) return;
    setWaiting((list) => list.filter((w) => w !== next));
    post.mutate(next.req);
  });

  const cancel = useMutation({
    mutationFn: async (id: number) => must(await api.POST("/jobs/{id}/cancel", { params: { path: { id } } })),
    onSuccess: (job) => {
      qc.setQueryData(["job", job.id], job);
      void qc.invalidateQueries({ queryKey: ["jobs", "action", cardId] });
    },
  });
  const settle = (id: number) => {
    remember(id);
    setSettled((s) => new Set(s).add(id));
  };

  return {
    actions,
    /** mine are the chosen model setup's actions. */
    mine: actions.filter((a) => a.instance_id === instance?.id),
    instances,
    instance,
    setInstance,
    runs: shown,
    waiting,
    started,
    lock,
    pending,
    report,
    start,
    stop: (id: number) => cancel.mutate(id),
    unwait: (key: number) => setWaiting((list) => list.filter((w) => w.key !== key)),
    settle,
  };
}

export type Forge = ReturnType<typeof useForge>;
