import { useSearchParams } from "react-router";

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

/** useLibrary is the current library: the URL's, else the last one used. */
export function useLibrary(): number {
  const [params] = useSearchParams();
  return libraryParam(params) ?? rememberedLibrary();
}

/** withLibrary adds ?library= to an app path, keeping any query it has. */
export function withLibrary(path: string, library: number): string {
  return `${path}${path.includes("?") ? "&" : "?"}library=${library}`;
}
