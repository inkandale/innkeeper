export function Page({ title }: { title: string }) {
  return (
    <section style={{ padding: "calc(var(--space) * 2)" }}>
      <h1>{title}</h1>
    </section>
  );
}
