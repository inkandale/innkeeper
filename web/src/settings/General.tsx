import { useQuery } from "@tanstack/react-query";
import { settingsQuery } from "../api/queries";
import { VectorsFrom } from "./VectorsFrom";
import { Theme } from "./Theme";

/** General holds the settings that apply to all of Innkeeper, and its version. */
export function General() {
  const settings = useQuery(settingsQuery);
  return (
    <div className="general">
      <VectorsFrom />
      <Theme />
      {settings.data && <p className="muted">Innkeeper {settings.data.version}</p>}
    </div>
  );
}
