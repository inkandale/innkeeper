/** themeLink returns the <link> that loads the selected theme, adding it on first use. */
export function themeLink(): HTMLLinkElement {
  let link = document.getElementById("theme") as HTMLLinkElement | null;
  if (!link) {
    link = document.createElement("link");
    link.id = "theme";
    link.rel = "stylesheet";
    link.href = "/api/theme.css";
    // Appended after the app's CSS, so the theme's rules win.
    document.head.append(link);
  }
  return link;
}

/** showTheme loads name's stylesheet, or turns the theme off for the default (""). */
export function showTheme(name: string) {
  const link = themeLink();
  link.disabled = name === "";
  if (name) link.href = `/api/themes/${encodeURIComponent(name)}.css`;
}
