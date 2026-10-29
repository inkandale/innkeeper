import { changedKeys } from "../forge/actions";
import { fieldChanges, fieldRows, type Subject } from "../forge/subject";
import { text, type Data } from "./draft";
import {
  afterGreetings,
  beforeGreetings,
  fieldLabel,
  greetingList,
} from "./Fields";

const greetings = (data: Data, key: string, noun: string) =>
  greetingList(data[key]).map((t, i) => ({
    title: `${noun} ${i + 1}`,
    text: t,
  }));

/** cardSubject is the Forge's subject on the card pages: a Review row per changed field, and a preview of its text fields in the Fields tab's order. */
export const cardSubject: Subject = {
  kind: "card",
  label: fieldLabel,
  jobFilter: (id) => ({ card_id: id }),
  placeholder:
    "Describe a change to this card… e.g. make the family robots · @cards/Billy is the brother",
  review: (start, draft, proposal) => {
    const keys = changedKeys(start, proposal);
    return {
      rows: fieldRows(start, draft, proposal, fieldLabel, keys),
      apply: (taken) => fieldChanges(proposal, keys, taken),
    };
  },
  preview: (data) => {
    const area = ([key, title]: [string, string]) => ({
      title,
      text: text(data[key]),
    });
    return [
      ...beforeGreetings.map(area),
      ...greetings(data, "alternate_greetings", "Alternate greeting"),
      ...greetings(data, "group_only_greetings", "Group-only greeting"),
      ...afterGreetings.map(area),
    ].filter((s) => s.text.trim());
  },
};
