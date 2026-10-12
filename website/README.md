# Websites

Two static Cloudflare Pages projects: the gg docs site and a browser replay.

Each one builds locally into a directory of files. Deploying the demo is separate. Nothing here configures DNS or adds a secret.

From the repository root, install and build one project with:

```bash
npm ci --prefix website/<project>
npm run build --prefix website/<project>
```

`npm run build` is the build. `npm ci` only installs dependencies.

| Directory | Pages project | Build (inside the directory) | Output directory | Hostname |
| --- | --- | --- | --- | --- |
| `website/docs` | `gg-docs` | `npm run build` | `website/docs/dist` | `https://gg.zunolab.dev` |
| `website/demo` | `gg-demo` | `npm run build` | `website/demo/dist` | `demo.zunolab.dev` (not attached yet) |

The `gg-docs` Pages project is served at https://gg.zunolab.dev (subdomain `gg-docs-ctl.pages.dev`).

After a demo build, from `website/demo`:

```bash
npx wrangler pages deploy dist --project-name gg-demo
```

Do not pass a custom domain flag for the demo. `demo.zunolab.dev` is not attached in this repo.

## docs

VitePress site. `npm run build` copies `README.md`, `README.zh-CN.md`, and the bilingual guides in `docs/` (including `docs/usage.md`) into a generated tree that is not committed, and rewrites relative links onto the site. `docs/demo.tape`, `SECURITY.md`, `LICENSE`, and `CONTRIBUTING.md` stay on GitHub. Chinese is the default locale; English is under `/en/`. Explicit heading anchors from the usage guide are kept, so links such as `/usage#web-and-electron` and `/en/architecture#security-model` still resolve. Mermaid fences, including the diagrams in the README and the architecture guide, render in the browser from the built assets. No model and no API key.

## demo

Static replay of gg sessions. Visitors pick a scenario and step through it, including approval prompts and the bash classifier denying a dangerous command under `--yolo` when there is no terminal. Tool-call lines are the runtime events `tool_call_start` and `tool_call_finish`. Approval and denial lines match the CLI text. The page does not call a model, does not embed an API key, and does not run a shell.
