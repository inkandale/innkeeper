import { changedKeys } from "../forge/actions";
import { fieldChanges, fieldRows, type Subject } from "../forge/subject";
import { fieldLabel } from "./Fields";

/** cardSubject is the Forge's subject on the card pages: a Review row per changed field. */
export const cardSubject: Subject = {
  kind: "card",
  label: fieldLabel,
  jobFilter: (id) => ({ card_id: id }),
  placeholder: "Reforge this card… e.g. make the family robots · @cards/Billy is the brother",
  review: (start, draft, proposal) => {
    const keys = changedKeys(start, proposal);
    return { rows: fieldRows(start, draft, proposal, fieldLabel, keys), apply: (taken) => fieldChanges(proposal, keys, taken) };
  },
};
