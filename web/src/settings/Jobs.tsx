import { useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { api, must } from "../api/client";
import { age, finished, jobStates, kindLabel, progressText } from "./jobUtil";

export function Jobs() {
  const [params, setParams] = useSearchParams();
  const status = jobStates.find((s) => s === params.get("status"));
  const list = useQuery({
    queryKey: ["jobs", status],
    queryFn: async () => must(await api.GET("/jobs", { params: { query: status ? { status } : {} } })),
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
          onChange={(e) => setParams(e.target.value ? { status: e.target.value } : {})}
        >
          <option value="">All states</option>
          {jobStates.map((s) => (
            <option key={s} value={s}>
              {s}
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
