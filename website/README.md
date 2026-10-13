# Websites

The gg docs site is a static Cloudflare Pages project.

It builds locally into a directory of files. Nothing here configures DNS or adds a secret.

From the repository root:

```bash
npm ci --prefix website/docs
npm run build --prefix website/docs
```

`npm run build` is the build. `npm ci` only installs dependencies.

| Directory | Pages project | Build (inside the directory) | Output directory | Hostname |
| --- | --- | --- | --- | --- |
| `website/docs` | `gg-docs` | `npm run build` | `website/docs/dist` | `https://gg.zunolab.dev` |

The `gg-docs` Pages project is served at https://gg.zunolab.dev (subdomain `gg-docs-ctl.pages.dev`).

## docs

VitePress site. `npm run build` copies `README.md`, `README.zh-CN.md`, and the bilingual guides in `docs/` (including `docs/usage.md`) into a generated tree that is not committed, and rewrites relative links onto the site. `docs/demo.tape`, `SECURITY.md`, `LICENSE`, and `CONTRIBUTING.md` stay on GitHub. Chinese is the default locale; English is under `/en/`. The Chinese search button and empty-result text are translated. Explicit heading anchors from the usage guide are kept, so links such as `/usage#web-and-electron` and `/en/architecture#security-model` still resolve. Mermaid fences, including the diagrams in the README and the architecture guide, render in the browser from the built assets. No model and no API key.

`npm run dev` syncs those sources first, then keeps copying them into the generated tree while VitePress is running. Edits to `README.md`, `README.zh-CN.md`, or a file under `docs/` show up without restarting the server.
