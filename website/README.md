# Websites

Three static sites for Cloudflare Pages. Each one builds locally into a directory of files. Deploying is separate: run `wrangler pages deploy` yourself. Nothing here configures a custom domain, DNS, or a secret.

From the repository root, install and build one project with:

```bash
npm ci --prefix website/<project>
npm run build --prefix website/<project>
```

`npm run build` is the build. `npm ci` only installs dependencies.

| Directory | Pages project | Build (inside the directory) | Output directory | Later hostname |
| --- | --- | --- | --- | --- |
| `website/home` | `zunolab-home` | `npm run build` | `website/home/dist` | `zunolab.dev` |
| `website/docs` | `gg-docs` | `npm run build` | `website/docs/dist` | `gg.zunolab.dev` |
| `website/demo` | `gg-demo` | `npm run build` | `website/demo/dist` | `demo.zunolab.dev` |

After a build, from that project directory:

```bash
npx wrangler pages deploy dist --project-name <pages-project>
```

Use `zunolab-home`, `gg-docs`, or `gg-demo` as `<pages-project>`. Do not pass a custom domain flag here. Attach `zunolab.dev`, `gg.zunolab.dev`, and `demo.zunolab.dev` later in the Pages project settings.

## home

Personal homepage. Chinese is the default; the header switches to English. The page is Juce, a Go backend engineer with 10 years of experience, formerly at ByteDance and JD.com, who built high-concurrency and commercialization systems and now works on AI agent engineering. [gg](https://github.com/hszjj221/gg) is the featured project. The tech blog section is reserved and empty.

## docs

VitePress site. `npm run build` copies `README.md`, `README.zh-CN.md`, and `docs/*` into a generated tree (not committed) and rewrites relative links onto the site. Chinese is the default locale; English is under `/en/`. Mermaid fences, including the diagram in the architecture guide, render in the browser from the built assets. No model and no API key.

## demo

Static replay of gg sessions. Visitors pick a scenario and step through it, including approval prompts and the bash classifier denying a dangerous command under `--yolo` when there is no terminal. Tool-call lines are the runtime events `tool_call_start` and `tool_call_finish`. Approval and denial lines match the CLI text. The page does not call a model, does not embed an API key, and does not run a shell.
