import "./Mark.css";

/** Mark is Innkeeper's logo: a roof over two character cards, the front one with a door. It draws in the text color. */
export function Mark({ size }: { size: number }) {
  return (
    <svg className="mark" viewBox="0 0 32 32" width={size} height={size} aria-hidden="true">
      <rect x="9" y="13" width="12" height="16" rx="2" fill="currentColor" opacity=".35" transform="rotate(-12 15 21)" />
      <path
        className="mark-card"
        fillRule="evenodd"
        d="M13 13h8a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-8a2 2 0 0 1-2-2V15a2 2 0 0 1 2-2zM15.3 29v-4.3a1.7 1.7 0 0 1 3.4 0V29z"
      />
      <path d="M3.5 15.5 16 4.5l12.5 11" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
