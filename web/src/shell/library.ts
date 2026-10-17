import { useQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { librariesQuery } from "../api/queries";

const libraryKey = "innkeeper.library";

const parse = (v: string | null) => (v && /^[1-9]\d*$/.test(v) ? Number(v) : undefined);

/** rememberedLibrary is the last library used, or 1. Storage can throw or be empty in private windows. */
export function rememberedLibrary(): number {
  try {
    return parse(localStorage.getItem(libraryKey)) ?? 1;
  } catch {
    return 1;
  }
}

export function rememberLibrary(id: number) {
  try {
    localStorage.setItem(libraryKey, String(id));
  } catch {
    // Without storage, the next visit starts in library 1.
  }
}

/** libraryParam is the URL's ?library=, if it names one. */
export const libraryParam = (p: URLSearchParams) => parse(p.get("library"));

/** useLibrary is the current library: the URL's, else the last one used, or 1 once the list shows it no longer exists. */
export function useLibrary(): number {
  const [params] = useSearchParams();
  const libraries = useQuery(librariesQuery).data;
  const library = libraryParam(params) ?? rememberedLibrary();
  return libraries && !libraries.some((l) => l.id === library) ? 1 : library;
}

/** withLibrary adds ?library= to an app path, keeping any query it has. */
export function withLibrary(path: string, library: number): string {
  return `${path}${path.includes("?") ? "&" : "?"}library=${library}`;
}
