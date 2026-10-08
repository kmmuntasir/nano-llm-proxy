# TypeScript Style Guide (frontend)

## Formatting

No formatter or linter is configured. Match the existing files: 2-space indent, **double quotes**, **no semicolons**, trailing commas in multiline literals, LF endings (`.editorconfig`). Keep lines readable (~100 cols).

## Files

- Components/pages: PascalCase matching the default export (`ProvidersPage.tsx`, `DataTable.tsx`).
- Non-component modules and Chakra snippets: lowercase/kebab-case (`client.ts`, `types.ts`, `color-mode.tsx`).
- One component per file (small private helper components in the same file are fine).

## Naming

- Types/interfaces PascalCase; API shapes end in `View` (`UserView`, `RuntimeSettingsView`) or describe the request/response; they live in `src/api/types.ts`.
- Functions/variables camelCase; booleans read as predicates (`isSuperadmin`).
- Constants SCREAMING_SNAKE_CASE when truly constant; local magic values get a named `const`.
- Acronyms consistent (`URL`, `ID`, `API`).

## Type Design

- `strict` on; explicit types for props and API payloads. Mirror Go JSON tags exactly — no invented or renamed fields.
- `verbatimModuleSyntax`: `import type` for type-only imports, always.
- `noUnusedLocals` / `noUnusedParameters` / `noFallthroughCasesInSwitch` break the build — no dead code.
- Avoid `any`; use concrete types or `unknown` + narrowing. Prefer string-literal unions over TS `enum`.
- Prefer `undefined` + optional chaining; match the backend's null/omitted behavior in `types.ts`.
- Add generics only when they earn their keep (`api<T>` does).

## React

- Functional components + hooks; early returns.
- Server data via TanStack Query (`useQuery`/`useMutation`, stable array `queryKey`s, invalidate after writes); local UI state via `useState`.
- Explicit prop types; Chakra style props, not inline `style`.
- Extract shared logic into a component under `components/` or a small hook; reuse before writing new.

## Async & Errors

- `async`/`await`; no floating promises. Calls go through `api/client.ts`.
- Errors are `ApiError`; show `error.message` via `toaster.create({ type: "error" })` or inline — never swallow.

## Imports

- Relative imports (no path alias is configured). Group: React → third-party → local; type imports as `import type`.
- No unused imports (build-breaking via `noUnusedLocals`).

## Comments

Only the *why* when non-obvious (see `api/client.ts` header). No comments that restate the code.

## Avoid

- `any`, TS `enum`/`namespace`, `console.log`.
- Magic numbers/strings inline; hardcoded URLs.
- New dependencies, a second HTTP client, a second state library.
