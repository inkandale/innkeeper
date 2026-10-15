const macro = /(\{\{[^{}]*\}\})/;
const macroOrStart = /(\{\{[^{}]*\}\}|<START>)/;

/** macroParts splits text into plain runs and macros; macros sit at odd indexes. start counts <START> as a macro, for example dialogue. */
export const macroParts = (s: string, start = false) => s.split(start ? macroOrStart : macro);
