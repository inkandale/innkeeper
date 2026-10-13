import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, upload } from "../api/client";
import { finished, progressText, useJobEvents, type Job } from "../settings/jobUtil";
import { pluginsQuery } from "../settings/Plugins";
import { useFinish } from "./Welcome";

/** JobLine is one job's live status, linking to its page. */
function JobLine({ label, id, to }: { label: string; id: number; to: string }) {
  const job = useQuery({
    queryKey: ["job", id],
    queryFn: async () => must(await api.GET("/jobs/{id}", { params: { path: { id } } })),
  });
  const j = job.data;
  useJobEvents(id, !!j && !finished(j));
  return (
    <p>
      <Link to={to}>
        {label}: {j?.status} {j && progressText(j)}
      </Link>
      {j?.error && <span className="error"> {j.error}</span>}
    </p>
  );
}

/** Upload is a file or page URL sent to an importer, and its job. */
type Upload = { key: number; name: string; job?: number; error?: string };

/** BringInCards syncs the apps that were set up and imports uploaded files and page URLs. */
export function BringInCards() {
  const qc = useQueryClient();
  const finish = useFinish();
  const plugins = useQuery(pluginsQuery);
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const [started, setStarted] = useState<Record<number, number>>({});
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [pageURL, setPageURL] = useState("");
  const nextKey = useRef(0);

  // Seeds the cache so the job line has a status before its first fetch.
  const track = (job: Job) => {
    qc.setQueryData(["job", job.id], job);
    return job.id;
  };
  const pull = useMutation({
    mutationFn: async (instance_id: number) => track(must(await api.POST("/reconciles", { body: { instance_id } }))),
    onSuccess: (job, id) => setStarted((s) => ({ ...s, [id]: job })),
  });
  // One file at a time: avatars can be large.
  const sendFiles = async (instance_id: number, files: File[]) => {
    const added = files.map((f) => ({ key: nextKey.current++, name: f.name }));
    setUploads((all) => [...all, ...added]);
    for (const [i, f] of files.entries()) {
      let u: Upload = added[i]!;
      try {
        u = { ...u, job: track(must(await api.POST("/imports", { body: { instance_id, params: { blob: await upload(f), name: f.name } } }))) };
      } catch (e) {
        u = { ...u, error: (e as Error).message };
      }
      setUploads((all) => all.map((x) => (x.key === u.key ? u : x)));
    }
  };
  const sendURL = async (instance_id: number, url: string) => {
    let u: Upload = { key: nextKey.current++, name: url };
    setUploads((all) => [...all, u]);
    try {
      u = { ...u, job: track(must(await api.POST("/imports", { body: { instance_id, params: { url } } }))) };
    } catch (e) {
      u = { ...u, error: (e as Error).message };
    }
    setUploads((all) => all.map((x) => (x.key === u.key ? u : x)));
  };

  const failed = plugins.error ?? instances.error;
  if (failed) return <p className="error">{failed.message}</p>;
  if (plugins.isPending || instances.isPending) return null;
  const pulls = new Set((plugins.data ?? []).filter((p) => p.capabilities?.includes("pull")).map((p) => p.name));
  const apps = (instances.data ?? []).filter((i) => pulls.has(i.plugin));
  const importer = (instances.data ?? []).find((i) => i.plugin === "card-import");
  const urlImporter = (instances.data ?? []).find((i) => i.plugin === "url-import");

  return (
    <div className="panel">
      <h2>Bring in cards</h2>
      {!apps.length && !importer && !urlImporter && (
        <p>
          Nothing to import from yet. <Link to="/welcome/plugins">Set up an app or the importer first.</Link>
        </p>
      )}
      {apps.map((i) => (
        <div key={i.id}>
          {started[i.id] ? (
            <JobLine label={i.name} id={started[i.id]!} to={`/sync/reconciles/${started[i.id]}`} />
          ) : (
            <button className="button" disabled={pull.isPending} onClick={() => pull.mutate(i.id)}>
              Sync {i.name}
            </button>
          )}
        </div>
      ))}
      {pull.error && <p className="error">{pull.error.message}</p>}
      {importer && (
        <label>
          Upload files{" "}
          <input
            type="file"
            multiple
            accept=".png,.json,.charx"
            disabled={uploads.some((u) => !u.job && !u.error)}
            onChange={(e) => {
              const files = [...(e.target.files ?? [])];
              e.target.value = "";
              void sendFiles(importer.id, files);
            }}
          />
        </label>
      )}
      {urlImporter && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void sendURL(urlImporter.id, pageURL.trim());
            setPageURL("");
          }}
        >
          <label>
            Import from a page URL{" "}
            <input
              type="url"
              required
              placeholder="https://chub.ai/characters/…"
              value={pageURL}
              onChange={(e) => setPageURL(e.target.value)}
            />
          </label>{" "}
          <button className="button" type="submit" disabled={uploads.some((u) => !u.job && !u.error)}>
            Import
          </button>
        </form>
      )}
      {uploads.map((u) =>
        u.job ? (
          <JobLine key={u.key} label={u.name} id={u.job} to={`/settings/jobs/${u.job}`} />
        ) : (
          <p key={u.key} className={u.error ? "error" : "muted"}>
            {u.name}: {u.error ?? "uploading…"}
          </p>
        ),
      )}
      <p className="muted">Imports keep running after you leave; Settings › Jobs shows them.</p>
      <button className="button primary" disabled={finish.isPending} onClick={() => finish.mutate()}>
        Finish
      </button>
      {finish.error && <p className="error">{finish.error.message}</p>}
    </div>
  );
}
