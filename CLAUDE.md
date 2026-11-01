# CLAUDE.md

Guidance for Claude Code working in this repository.
Read [AGENTS.md](AGENTS.md) first — it documents commands, repo map,
invariants, and conventions that apply to any coding agent.

## Quick orientation

- **What this is:** Go + Fiber v3 + templ + Svelte 5 meta-framework, Phase 1 (routing, SSR/CSR rendering, persistent layouts, soft navigation). `phase1-implementation-blueprint.md` is the binding spec; its §0 decisions are not open to reinterpretation.
- **Two runtimes, one boundary:** `app/*.templ` renders server-side; `frontend/*.ts` mounts Svelte client-side. They meet only through the navigation envelope protocol (`frontend/types.ts`, `internal/render/navigation.go`).
- **Gate discipline:** work proceeds gate-by-gate (E→F→G, blueprint §5). Each gate has exact acceptance criteria; implement one gate, verify fully, stop. Commit prefix = gate ID.

## Workflow

1. Before a task: read the gate's blueprint section, then the modules it touches. The invariants in AGENTS.md (ownership boundary, mounted-vs-declared, trust boundary, fallback policy) explain *why* the code looks the way it does — violating them silently breaks real-browser behavior while unit tests stay green.
2. After changes: run `make verify`. It runs vet, Go tests, svelte-check, tsc, vitest, gofmt, git-diff hygiene, production build, and Chromium E2E. Do not claim done on partial passes; report failures as failures.
3. Real-browser checks matter here: happy-dom cannot catch layout remounts, event-trust issues, or reload-instead-of-soft-nav bugs. If behavior differs between vitest and Chromium, trust Chromium and use `?go-meta-debug=1` + `window.__goMetaNavLog` to see which pipeline branch ran.

## Commit style

Follow the existing history: `G05: child-only page swap inside a retained persistent layout` — prefix with gate ID, lowercase imperative summary, body explaining what/why including non-obvious context (bugs found, design trade-offs). End with the Claude Code attribution line.

## Don'ts

- Never edit `generated/` or `frontend/route-registry.generated.ts`; regenerate instead and expect zero drift.
- No new npm/go dependencies unless genuinely necessary.
- Don't refactor beyond the current gate's scope; G06–G08 own subtree exit, re-entry state, and return-navigation acceptance.
- No server-side JS, no Svelte SSR — client-only Svelte is a binding decision.
