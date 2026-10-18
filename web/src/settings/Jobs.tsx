import { useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { api, must } from "../api/client";
import { instancesQuery, pluginsQuery } from "../api/queries";
import { age, finished, jobStates, kindLabel, progressText } from "./jobUtil";

export function Jobs() {
  const [params, setParams] = useSearchParams();
  const status = jobStates.find((s) => s === params.get("status"));
  const plugin = params.get("plugin") ?? "";
  const plugins = useQuery(pluginsQuery);
  const instances = useQuery(instancesQuery);
  const label = new Map((instances.data ?? []).map((i) => [i.id, i.label]));
  const filter = (key: string, value: string) =>
    setParams((p) => {
      if (value) p.set(key, value);
      else p.delete(key);
      return p;
    });
  const list = useQuery({
    queryKey: ["jobs", status, plugin],
    queryFn: async () =>
      must(await api.GET("/jobs", { params: { query: { ...(status ? { status } : {}), ...(plugin ? { plugin } : {}) } } })),
    // ponytail: polls while jobs run; a stream per row would use up the browser's six connections per host.
    refetchInterval: (q) => (q.state.data?.some((j) => !finished(j)) ? 2000 : false),
  });
  return (
    <section>
      <div className="toolbar">
        <select
          className="input"
          aria-label="State"
          value={status ?? ""}
          onChange={(e) => filter("status", e.target.value)}
        >
          <option value="">All states</option>
          {jobStates.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <select className="input" aria-label="Plugin" value={plugin} onChange={(e) => filter("plugin", e.target.value)}>
          <option value="">All plugins</option>
          {(plugins.data ?? []).map((p) => (
            <option key={p.name} value={p.name}>
              {p.name}
            </option>
          ))}
        </select>
      </div>
      {list.isError && <p className="error">{list.error.message}</p>}
      <table className="jobs">
        <thead>
          <tr>
            <th>Job</th>
            <th>Kind</th>
            <th>Instance</th>
            <th>State</th>
            <th>Progress</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {(list.data ?? []).map((j) => (
            <tr key={j.id}>
              <td>
                <Link to={`/settings/jobs/${j.id}`}>#{j.id}</Link>
              </td>
              <td>{kindLabel(j)}</td>
              <td>{j.instance_id ? (label.get(j.instance_id) ?? `#${j.instance_id}`) : ""}</td>
              <td className={"state " + j.status}>{j.status}</td>
              <td>{progressText(j)}</td>
              <td>{age(j.created_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
