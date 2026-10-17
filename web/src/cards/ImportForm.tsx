import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, upload, withCapability } from "../api/client";
import { progressText, useJob, type Job } from "../settings/jobUtil";

type Plugin = { name: string; capabilities?: string[] | null; inputs?: string[] | null };
type Instance = { id: number; plugin: string };

/** Importers are the import instances that take files and page URLs, if any. */
export type Importers = { file?: Instance; url?: Instance };

/** importers picks, for each input, the first import instance whose manifest declares it. */
export function importers(instances: Instance[] | null | undefined, plugins: Plugin[] | null | undefined): Importers {
  const inputs = new Map((plugins ?? []).map((p) => [p.name, p.inputs ?? []]));
  const all = withCapability(instances, plugins, "import");
  return {
    file: all.find((i) => inputs.get(i.plugin)?.includes("file")),
    url: all.find((i) => inputs.get(i.plugin)?.includes("url")),
  };
}

/** JobLine is one job's live status, linking to its page. */
export function JobLine({ label, id, to }: { label: string; id: number; to: string }) {
  const j = useJob(id).data;
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

/** ImportForm imports uploaded files and page URLs into library, and lists each one's job. */
export function ImportForm({ file, url, library }: Importers & { library: number }) {
  const qc = useQueryClient();
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [pageURL, setPageURL] = useState("");
  const nextKey = useRef(0);

  // Seeds the cache so the job line has a status before its first fetch.
  const track = (job: Job) => {
    qc.setQueryData(["job", job.id], job);
    return job.id;
  };
  // One file at a time: avatars can be large.
  const sendFiles = async (instance_id: number, files: File[]) => {
    const added = files.map((f) => ({ key: nextKey.current++, name: f.name }));
    setUploads((all) => [...all, ...added]);
    for (const [i, f] of files.entries()) {
      let u: Upload = added[i]!;
      try {
        u = { ...u, job: track(must(await api.POST("/imports", { body: { instance_id, library, params: { blob: await upload(f), name: f.name } } }))) };
      } catch (e) {
        u = { ...u, error: (e as Error).message };
      }
      setUploads((all) => all.map((x) => (x.key === u.key ? u : x)));
    }
  };
  const sendURL = async (instance_id: number, page: string) => {
    let u: Upload = { key: nextKey.current++, name: page };
    setUploads((all) => [...all, u]);
    try {
      u = { ...u, job: track(must(await api.POST("/imports", { body: { instance_id, library, params: { url: page } } }))) };
    } catch (e) {
      u = { ...u, error: (e as Error).message };
    }
    setUploads((all) => all.map((x) => (x.key === u.key ? u : x)));
  };
  const sending = uploads.some((u) => !u.job && !u.error);

  return (
    <>
      {file && (
        <label>
          Upload files{" "}
          <input
            type="file"
            multiple
            accept=".png,.json,.charx"
            disabled={sending}
            onChange={(e) => {
              const files = [...(e.target.files ?? [])];
              e.target.value = "";
              void sendFiles(file.id, files);
            }}
          />
        </label>
      )}
      {url && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void sendURL(url.id, pageURL.trim());
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
          <button className="button" type="submit" disabled={sending}>
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
    </>
  );
}
