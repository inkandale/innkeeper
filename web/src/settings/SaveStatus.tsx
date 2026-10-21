/** SaveStatus sits next to a control that saves on change: "Saved" after each save, then fades, or the save's error. */
export function SaveStatus({ save }: { save: { isSuccess: boolean; error: Error | null; submittedAt: number } }) {
  if (save.error) return <span className="error">{save.error.message}</span>;
  if (!save.isSuccess) return null;
  // The key restarts the fade on every save.
  return (
    <span key={save.submittedAt} className="saved" role="status">
      <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2">
        <path d="M3 8.5l3 3 7-7" />
      </svg>
      Saved
    </span>
  );
}
