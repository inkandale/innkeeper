import { useQuery } from "@tanstack/react-query";
import { settingsQuery } from "../api/queries";
import { EmbedFrom } from "./EmbedFrom";
import { Theme } from "./Theme";

/** General holds the settings that apply to all of Innkeeper, and its version. */
export function General() {
  const settings = useQuery(settingsQuery);
  return (
    <div className="general">
      <EmbedFrom />
      <Theme />
      {settings.data && <p className="muted">Innkeeper {settings.data.version}</p>}
    </div>
  );
}
