/** Loading stands in for a page while its query is pending, so the page doesn't sit blank. */
export function Loading() {
  return (
    <p className="loading" role="status" aria-busy="true">
      Loading…
    </p>
  );
}
