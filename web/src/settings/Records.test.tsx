import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toasts } from "../shell/Toasts";
import { Records, type RecordType } from "./Records";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const skills: RecordType = {
  label: "Skills",
  import: ["file", "url"],
  schema: {
    type: "object",
    required: ["name"],
    properties: { name: { type: "string", title: "Name" }, body: { type: "string", title: "Body", contentMediaType: "text/markdown" } },
  },
};
const base = "/api/instances/5/records/skills";

let sent: { method: string; path: string; search: string; type: string | null; body: string }[];
let stored: Record<string, { name: string; body?: string }>;
beforeEach(() => {
  sent = [];
  stored = { college: { name: "college", body: "Campus." } };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      const body = await req.text();
      sent.push({ method: req.method, path: u.pathname, search: u.search, type: req.headers.get("Content-Type"), body });
      const id = decodeURIComponent(u.pathname.slice(base.length + 1));
      if (req.method === "GET" && u.pathname === base)
        return json(Object.entries(stored).map(([id, r]) => ({ id, name: r.name, description: r.body })));
      if (req.method === "GET") return stored[id] ? json({ id, record: stored[id] }) : json({ detail: "not found" }, 404);
      if (req.method === "POST" && u.pathname === base) {
        const rec = JSON.parse(body).record;
        if (!rec.name) return json({ detail: "a skill needs a name" }, 400);
        stored[rec.name] = rec;
        return json({ id: rec.name }, 201);
      }
      if (req.method === "PUT") {
        const rec = JSON.parse(body).record;
        delete stored[id];
        stored[rec.name] = rec;
        return json({ id: rec.name });
      }
      if (req.method === "DELETE") {
        delete stored[id];
        return new Response(null, { status: 204 });
      }
      const name = u.pathname.endsWith("/import-url") ? "noir" : "upload";
      stored[name] = { name };
      return json([{ id: name, name }]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderRecords(recordType = skills) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Toasts>
        <Records instance={5} type="skills" recordType={recordType} />
      </Toasts>
    </QueryClientProvider>,
  );
}

test("lists records under the type's label", async () => {
  renderRecords();
  expect(screen.getByRole("heading", { name: "Skills" })).toBeInTheDocument();
  expect(await screen.findByText("college")).toBeInTheDocument();
  expect(screen.getByText("Campus.")).toBeInTheDocument();
});

test("New creates a record from the schema form", async () => {
  const user = userEvent.setup();
  renderRecords();
  await screen.findByText("college");
  await user.click(screen.getByRole("button", { name: "New" }));
  await user.type(screen.getByLabelText("Name"), "noir");
  await user.type(screen.getByLabelText("Body"), "Rain.");
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByText("noir")).toBeInTheDocument();
  expect(sent.find((r) => r.method === "POST")?.body).toBe(JSON.stringify({ record: { name: "noir", body: "Rain." } }));
  expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
});

test("shows why the plugin refused a record", async () => {
  const user = userEvent.setup();
  renderRecords();
  await screen.findByText("college");
  await user.click(screen.getByRole("button", { name: "New" }));
  await user.type(screen.getByLabelText("Body"), "Nameless.");
  // The browser would block the empty required field; the plugin's answer is what's under test.
  screen.getByLabelText("Name").removeAttribute("required");
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByText("a skill needs a name")).toBeInTheDocument();
});

test("Edit loads the record and replaces it", async () => {
  const user = userEvent.setup();
  renderRecords();
  await user.click(await screen.findByRole("button", { name: "Edit college" }));
  const body = await screen.findByLabelText("Body");
  expect(body).toHaveValue("Campus.");
  await user.clear(body);
  await user.type(body, "Dorms.");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(sent.some((r) => r.method === "PUT")).toBe(true));
  const put = sent.find((r) => r.method === "PUT")!;
  expect(put.path).toBe(base + "/college");
  expect(JSON.parse(put.body)).toEqual({ record: { name: "college", body: "Dorms." } });
});

test("Delete asks first", async () => {
  const user = userEvent.setup();
  const confirm = vi.fn(() => true);
  vi.stubGlobal("confirm", confirm);
  renderRecords();
  await user.click(await screen.findByRole("button", { name: "Delete college" }));
  expect(confirm).toHaveBeenCalledWith("Delete college?");
  await waitFor(() => expect(screen.queryByText("college")).not.toBeInTheDocument());
  expect(sent.some((r) => r.method === "DELETE" && r.path === base + "/college")).toBe(true);
});

test("imports a file with its name, and a URL", async () => {
  const user = userEvent.setup();
  renderRecords();
  await screen.findByText("college");
  await user.upload(screen.getByLabelText("Import a file"), new File(["# Skill"], "SKILL.md"));
  expect(await screen.findByText("upload")).toBeInTheDocument();
  const file = sent.find((r) => r.path === base + "/import")!;
  expect(file).toMatchObject({ search: "?name=SKILL.md", type: "application/octet-stream", body: "# Skill" });

  await user.type(screen.getByLabelText("Import from a URL"), "https://example.com/noir.zip");
  await user.click(screen.getByRole("button", { name: "Import" }));
  expect(await screen.findByText("noir")).toBeInTheDocument();
  expect(JSON.parse(sent.find((r) => r.path === base + "/import-url")!.body)).toEqual({ url: "https://example.com/noir.zip" });
});

test("a type without import offers none", async () => {
  renderRecords({ ...skills, import: undefined });
  const list = await screen.findByRole("list");
  expect(within(list).getByText("college")).toBeInTheDocument();
  expect(screen.queryByLabelText("Import a file")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Import from a URL")).not.toBeInTheDocument();
});
