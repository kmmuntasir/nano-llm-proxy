# Frontend Development Rules

## General

The admin GUI lives in `web/` (package `nano-llm-proxy-web`): React 19 + TypeScript strict + Vite + **Chakra UI v3** + **TanStack Query** + React Router (`react-router-dom` v7, `BrowserRouter`) + `lucide-react` icons + Recharts. Built to `web/dist/`, embedded in the Go binary under the `prod` tag and served by the gateway itself (same origin, cookie session).

**There is no lint script, no formatter config, and no test framework.** The done-signal for any change is `cd web && npm run build` (`tsc -b && vite build`). Do not invent test or lint commands; introducing any is a deliberate project decision — propose it first.

## Project Structure

```
web/src/
    main.tsx            # QueryClientProvider → BrowserRouter → Chakra Provider → App + Toaster
    App.tsx             # session gate (useSession → /api/auth/me) + the whole route tree; superadmin-only routes
    api/client.ts       # fetch wrapper: api/post/put/patch/del, ApiError, 401 → setUnauthorizedHandler
    api/types.ts        # *View / request / response types mirroring the Go JSON exactly
    components/         # AppLayout, DataTable, ModelCard, UsageCharts, setup dialogs, StatusBadge, ...
    components/ui/      # Chakra snippets (provider, toaster, dialog, color-mode, password-input, close-button)
    pages/              # one route container per page: Dashboard, Models, Usage, Providers, Users, Settings, Profile, MyKeys, Docs, Login
    assets/             # logo files
web/public/             # static files (favicon)
```

## File & Component Conventions

- Component and page files are **PascalCase** matching the default export (`ProvidersPage.tsx`, `DataTable.tsx`); `components/ui/` snippets and non-component modules (`client.ts`, `types.ts`) are lowercase/kebab-case. Follow the folder you're in.
- Functional components + hooks only; default export for pages/components, named exports for hooks and helpers.
- Style is the existing style: 2-space indent, **double quotes, no semicolons**, trailing commas. There is no formatter — match the file.
- `verbatimModuleSyntax` is on: use `import type` for type-only imports. `noUnusedLocals`/`noUnusedParameters` are build-breaking.
- Early returns over nested branches. Reuse `components/` (e.g. `DataTable`, `ConfirmDialog`, `CopyButton`, `JsonBlock`, `StatusBadge`) before writing new markup.
- `components/ui/` are Chakra CLI snippets — extend by wrapping; don't restyle them ad hoc.

## State Management

- **Server state: TanStack Query.** `useQuery` with a stable `queryKey` array (`["me"]`, `["models"]`, `["usage", "summary", from, to]`); `useMutation` + `queryClient.invalidateQueries` after writes. This is the established pattern — no Redux/Zustand/Jotai, no second cache layer.
- **Session:** `useSession()` in `App.tsx` (`["me"]`) is the single source of "who am I"; role gating reads `user.role`.
- **Local UI state:** `useState`. **URL state:** React Router.
- Forms are plain controlled inputs + `useState` + a mutation; no form library is installed. Don't add one for a single form.

## Routing & Guards

- One route tree in `App.tsx`. Unauthenticated → only `/login`; authenticated → `AppLayout` + routes; `users`/`providers`/`settings` render only for `role === "superadmin"`. Unknown paths redirect to `/`.
- UI gating is convenience; the Go handlers (`requireSession`, `requireSuperadmin`) are the boundary. A new admin page needs the backend check first.
- Pages are statically imported; no lazy loading ad hoc (bundle ships inside the binary).

## API Layer

- All HTTP goes through `api/client.ts` (`api`, `post`, `put`, `patch`, `del`) — same-origin, `credentials: "include"`. No raw `fetch` in components, no axios, no new HTTP library.
- Errors are `ApiError(status, message)`; the message comes from the backend's `{"error":{"message":"…"}}`. Surface via `toaster.create({ title, type: "error" })` (`components/ui/toaster`) or inline text — never swallow.
- A 401 anywhere calls the registered unauthorized handler → `/login`. Don't add token refresh or a second interceptor; expiry means re-login.
- Mirror backend JSON exactly in `api/types.ts` (field names are the Go JSON tags); never invent fields. When a Go response shape changes, update `types.ts` in the same change.
- Dev server (`npm run dev`, :5173) proxies `/api`, `/v1`, `/health` to `127.0.0.1:8787` (`vite.config.ts`). Never hardcode hosts.
- The browser never sees upstream provider keys; client keys are shown in plaintext exactly once (`KeyRevealDialog`) — keep that flow.

## Styling — Chakra UI v3

- Compose Chakra components and style props (`Box`, `HStack`, `VStack`, `Card`, `Field`, `NativeSelect`, `Switch`, …) and semantic tokens; no Tailwind, no CSS files, no `style={{}}` where a style prop exists, no raw hex where a token exists.
- Light/dark via `next-themes` (`components/ui/color-mode`) — keep both modes working.
- Icons from `lucide-react`; charts from Recharts (`UsageCharts`).
- Mobile widths matter: pages are used on narrow viewports; avoid fixed widths and let long values wrap.

## UI Copy

Short, factual, no marketing. Toasts: `toaster.create({ title: "Provider added", type: "success" })`. Destructive actions go through `ConfirmDialog` before executing. Docs/Guide copy lives in `pages/DocsPage.tsx` and must match `docs/` and the actual behavior.

## Environment

The GUI reads no build-time env. Anything configurable belongs in the gateway's settings (`/api/settings`), not `import.meta.env`.

## Build and Run

```bash
cd web
npm ci
npm run dev        # Vite on :5173, proxying the gateway on :8787
npm run build      # tsc -b && vite build → web/dist/  (the verification gate)
npm run preview
```

After a GUI change that must ship in the binary: rebuild `web/dist`, then `go build -tags prod` (or `deploy.sh`). `web/dist/`, `node_modules/`, `tsconfig.tsbuildinfo` are never committed.

## Avoid

- Tailwind, shadcn, axios, react-hook-form/yup/zod, Redux/Zustand, a second global context, a second toast/modal system.
- Raw `fetch`, hardcoded URLs, `console.log`, `any` (use real types or `unknown` + narrowing).
- New npm dependencies without justification — bundle size is part of the binary size.
- Inventing test/lint tooling silently; committing build output.
