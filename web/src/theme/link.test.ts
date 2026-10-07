import { showTheme, themeLink } from "./link";

test("showTheme previews a theme and turns the link off for the default", () => {
  expect(themeLink().getAttribute("href")).toBe("/api/theme.css");
  showTheme("dusk");
  expect(themeLink().getAttribute("href")).toBe("/api/themes/dusk.css");
  expect(themeLink().disabled).toBe(false);
  showTheme("");
  expect(themeLink().disabled).toBe(true);
  expect(document.querySelectorAll("#theme")).toHaveLength(1);
});
