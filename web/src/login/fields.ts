export type Field = { name: string; label: string; type: "text" | "password"; required: boolean; autoComplete: string };

type Schema = {
  properties?: Record<string, { title?: string; format?: string; writeOnly?: boolean }>;
  required?: string[];
};

/** formFields lists a login form schema's fields as text inputs. */
export function formFields(schema: unknown): Field[] {
  const s = schema as Schema;
  return Object.entries(s.properties ?? {}).map(([name, p]) => ({
    name,
    label: p.title ?? name,
    type: p.format === "password" || p.writeOnly || name === "password" ? "password" : "text",
    required: s.required?.includes(name) ?? false,
    autoComplete:
      p.format === "password" || name === "password" ? "current-password" : p.writeOnly ? "one-time-code" : "username",
  }));
}
