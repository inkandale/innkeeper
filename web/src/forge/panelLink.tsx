import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { Data } from "../card/draft";

/** OpenDraft is the draft of the card open in a Forge: its data, unsaved edits included, and the revision it started from. */
export type OpenDraft = { card: number; revision: number; data: Data };

/**
 * PanelProposal is changed card fields a plugin panel sent for Review. start is the draft the panel last read, when that
 * was this card at revision. label names the panel.
 */
export type PanelProposal = { key: number; card: number; revision: number; start?: Data; proposal: Record<string, string>; label: string };

type PanelLink = {
  /** draft is the open Forge's draft, if a card's Forge is open. */
  draft: () => OpenDraft | undefined;
  /** publish shares card's draft, or with no draft stops sharing it. */
  publish: (card: number, draft?: OpenDraft) => void;
  proposal?: PanelProposal;
  propose: (p: Omit<PanelProposal, "key">) => void;
  /** drop lets go of the proposal with key once Review settles it. */
  drop: (key: number) => void;
};

// Without a provider, as for a page rendered alone in a test, nothing is shared.
const PanelLinkContext = createContext<PanelLink>({ draft: () => undefined, publish: () => {}, propose: () => {}, drop: () => {} });

/** PanelLinkProvider lets the open Forge and plugin panels, which sit apart in the Shell, share the draft and proposals. */
export function PanelLinkProvider({ children }: { children: ReactNode }) {
  // A ref, not state: the draft changes on every keystroke, and only a panel's host.context reads it.
  const draft = useRef<OpenDraft | undefined>(undefined);
  const keys = useRef(0);
  const [proposal, setProposal] = useState<PanelProposal>();
  const value = useMemo<PanelLink>(
    () => ({
      draft: () => draft.current,
      publish: (card, d) => {
        if (d) draft.current = d;
        else if (draft.current?.card === card) draft.current = undefined;
      },
      proposal,
      propose: (p) => setProposal({ ...p, key: ++keys.current }),
      drop: (key) => setProposal((p) => (p?.key === key ? undefined : p)),
    }),
    [proposal],
  );
  return <PanelLinkContext.Provider value={value}>{children}</PanelLinkContext.Provider>;
}

export const usePanelLink = () => useContext(PanelLinkContext);

/** usePublishDraft shares d while the calling Forge is mounted. */
export function usePublishDraft(d: OpenDraft | undefined) {
  const { publish } = usePanelLink();
  const card = d?.card;
  const revision = d?.revision;
  const data = d?.data;
  useEffect(() => {
    if (card === undefined || revision === undefined || data === undefined) return;
    publish(card, { card, revision, data });
    return () => publish(card);
  }, [publish, card, revision, data]);
}
