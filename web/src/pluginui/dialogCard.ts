import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../api/client";
import { entityQuery } from "../api/queries";
import { text, type Data } from "../card/draft";
import { useLibrary } from "../shell/library";

/** useDialogCard loads the card a panel asked about: name once it's fit to act on, or problem saying why it isn't. */
export function useDialogCard(card: number): { name?: string; problem?: string } {
  const library = useLibrary();
  const entity = useQuery(entityQuery("card", card));
  if (entity.isError) return { problem: entity.error instanceof ApiError && entity.error.status === 404 ? "This card doesn't exist." : entity.error.message };
  if (!entity.data) return {};
  if (entity.data.library_id !== library) return { problem: "This card is in another library." };
  return { name: text((entity.data.revision.data as Data).name) || `#${card}` };
}
