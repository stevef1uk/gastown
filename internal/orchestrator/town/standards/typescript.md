## Town Hall Coding Standards (TypeScript / JavaScript / Web)

- **Runtime & module boundaries:** For vanilla browser JS (`app.js`, `index.html`), use plain script globals and `fetch()`—do not use bundler-only ES `import`/`export` unless `package.json` configures a build step.
- **DOM & API contract:** Match DOM element IDs, CSS selectors, and `fetch()` paths/methods verbatim with `index.html`, `architecture.md`, and `SPEC.md`. Never call backend/DB symbols from frontend code.
- **TypeScript & React/Next.js:** Type all component props explicitly (no implicit `any`). Wire new components into the page/layout entrypoint.
- **Frontend tests:** Start Jest DOM test files (`*.test.ts`, `*.test.tsx`) with `// @jest-environment jsdom` and mock `fetch` / `EventSource` deterministically.
