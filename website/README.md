# Personal homepage

This branch only contains the homepage. The gg docs site and the replay demo are not here. Do not merge this until the homepage is ready to publish.

Nothing here deploys, configures DNS, or adds a secret.

| | |
| --- | --- |
| Directory | `website/home` |
| Pages project | `zunolab-home` |
| Build command | `npm run build` |
| Output directory | `website/home/dist` |
| Later hostname | `zunolab.dev` |

From the repository root:

```bash
npm ci --prefix website/home
npm run build --prefix website/home
```

`npm run build` is the build. `npm ci` only installs dependencies.

After a build, from `website/home`:

```bash
npx wrangler pages deploy dist --project-name zunolab-home
```

Do not pass a custom domain flag. `zunolab.dev` is not attached in this repo.

The page is Chinese by default, with an English toggle. It is Juce, a Go backend engineer with 10 years of experience, formerly at ByteDance and JD.com, who built high-concurrency and commercialization systems and now works on AI agent engineering. [gg](https://github.com/hszjj221/gg) is the featured project. The tech blog section is reserved and empty.
