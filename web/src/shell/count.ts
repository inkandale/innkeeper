/** count is n with noun, plural unless n is 1: "1 card", "3 cards". */
export const count = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;
