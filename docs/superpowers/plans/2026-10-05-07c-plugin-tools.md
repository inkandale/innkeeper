# Plugin MCP Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let plugins add their own MCP tools, each with a tier, served at `/mcp` and through the API.

**Architecture:** A plugin with the `mcp` capability answers `mcp.tools` and `mcp.call`. The API gains `GET /plugin-tools`, which lists every working instance's tools, and `POST /instances/{id}/tools/{name}`, which calls one after checking the caller's tier against the tool's. `/mcp` builds each request's server from the built-in tools plus the plugin tools the caller's tier allows, and plugin tools call the API route, so tiers apply the same way.

**Tech Stack:** Go 1.27, the MCP SDK from plan 7b. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-05-mcp-server-design.md`, "Plugin tools". Build order step 3.

**Kaneo:** INN-7.

---

## Design notes

- **Protocol.** `mcp.tools` replies `{"tools": [{"name", "description", "input_schema", "tier"}]}`. Names match `^[a-z0-9_]{1,40}$`. A missing or unknown `tier` is `destructive`. `mcp.call` takes `{"name", "arguments"}` and replies `{"content": [{"type": "text", "text"}], "is_error"}`. A JSON-RPC error is a tool error too.
- **Cache.** Stateless MCP builds a server per request, so the API caches each instance's tool list for a minute, and drops it when the instance changes. A plugin's tools are fixed per version and config, so a minute's staleness only delays a new tool.
- **Names.** `/mcp` names a plugin tool `<instance>_<tool>`: the instance name lowercased, other characters as `_`, runs collapsed, trimmed to 20. A name that collides, or a built-in's name, gets `_<instance id>` before the tool name. Names over 64 characters are skipped with a log line.
- **Tier checks.** `POST /instances/{id}/tools/{name}` is `read` in route metadata, the lowest a tool can need. The handler looks up the tool's tier and refuses the caller with `403` when it's lower. A tool the plugin doesn't list is `404`.
- **Failures.** A broken instance or one whose plugin fails `mcp.tools` contributes no tools, and logs why. A call to a plugin that crashes or times out (2 minutes) is a tool error saying the plugin is unavailable.
- **Contract kit.** For the `mcp` capability, `mcp.tools` must answer within 5 seconds with valid names, tiers, and object input schemas.

## Tasks

### Task 1: Protocol

- [ ] `protocol/mcp.go`: methods, types, `Tool.Validate`, `TierOf` (unknown → `destructive`).
- [ ] `docs/protocol.md`: the `mcp` capability.
- [ ] Contract kit check and its test.
- [ ] Commit.

### Task 2: Fake plugin tools

- [ ] The fake plugin answers `mcp.tools` with `echo` (read), `note` (write), `wipe` (destructive), `untiered` (no tier), and `crash`, and `mcp.call` for each.
- [ ] Commit.

### Task 3: API routes

- [ ] Tests: `GET /plugin-tools` lists the fake instance's tools with tiers; a `read` token calls `echo` but gets `403` on `note` and on `untiered`; an unknown tool is `404`; `crash` is a `502`-style error; editing the instance drops the cache.
- [ ] Implement in `internal/api/tools.go`.
- [ ] Commit.

### Task 4: MCP

- [ ] Tests: a `read` client lists `fake_echo` and not `fake_note`; calling it returns the plugin's text; naming with two instances named alike; a built-in name collision.
- [ ] Per-request server: built-in tools for the tier plus plugin tools from `GET /plugin-tools` with the caller's token.
- [ ] README and the spec's "Plugin tools" if anything moved.
- [ ] Commit.

## Plan series

- **Lumiverse plugin:** candidate tools, each its own small change in `innkeeper-plugins`: list characters Innkeeper doesn't track (read), make a regex script global (write in Lumiverse, so `destructive` from Innkeeper's view).
