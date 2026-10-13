import { defineConfig } from "vitepress";
import { withMermaid } from "vitepress-plugin-mermaid";
import { docs } from "../pages.mjs";

const zhSidebar = [
  { text: "概览", link: "/" },
  ...docs.map((doc) => ({ text: doc.zh, link: `/${doc.name}` })),
];

const enSidebar = [
  { text: "Overview", link: "/en/" },
  ...docs.map((doc) => ({ text: doc.en, link: `/en/${doc.name}` })),
];

const github = "https://github.com/hszjj221/gg";

export default withMermaid(
  defineConfig({
    title: "gg",
    description: "gg 文档，由仓库 README 与 docs/ 生成。",
    lang: "zh-CN",
    cleanUrls: true,
    srcDir: "src",
    outDir: "dist",
    lastUpdated: false,
    ignoreDeadLinks: false,
    sitemap: { hostname: "https://gg.zunolab.dev" },
    head: [["link", { rel: "icon", href: "/favicon.svg" }]],
    locales: {
      root: {
        label: "简体中文",
        lang: "zh-CN",
        description: "gg 文档，由仓库中的 README.zh-CN.md 与 docs/*.zh-CN.md 生成。",
        themeConfig: {
          nav: [
            { text: "文档", link: "/" },
            { text: "GitHub", link: github },
          ],
          sidebar: zhSidebar,
          outline: { label: "本页" },
          docFooter: { prev: "上一页", next: "下一页" },
          darkModeSwitchLabel: "外观",
          lightModeSwitchTitle: "浅色",
          darkModeSwitchTitle: "深色",
          sidebarMenuLabel: "目录",
          returnToTopLabel: "回到顶部",
          langMenuLabel: "语言",
          footer: {
            message: "由 gg 仓库的 README 与 docs/ 生成，构建时同步。",
            copyright: "MIT",
          },
        },
      },
      en: {
        label: "English",
        lang: "en",
        description: "gg docs, generated from the repository README and docs/.",
        themeConfig: {
          nav: [
            { text: "Docs", link: "/en/" },
            { text: "GitHub", link: github },
          ],
          sidebar: enSidebar,
          outline: { label: "On this page" },
          footer: {
            message: "Generated from README and docs/ in the gg repository at build time.",
            copyright: "MIT",
          },
        },
      },
    },
    themeConfig: {
      search: {
        provider: "local",
        options: {
          locales: {
            root: {
              translations: {
                button: {
                  buttonText: "搜索",
                  buttonAriaLabel: "搜索",
                },
                modal: {
                  displayDetails: "显示详细列表",
                  resetButtonTitle: "重置搜索",
                  backButtonTitle: "关闭搜索",
                  noResultsText: "没有找到",
                  footer: {
                    selectText: "选择",
                    selectKeyAriaLabel: "回车",
                    navigateText: "切换",
                    navigateUpKeyAriaLabel: "上箭头",
                    navigateDownKeyAriaLabel: "下箭头",
                    closeText: "关闭",
                    closeKeyAriaLabel: "esc",
                  },
                },
              },
            },
          },
        },
      },
      socialLinks: [{ icon: "github", link: github }],
    },
    markdown: {
      config(md) {
        // The guides mention paths like mcp_<server>_<tool>. Those are
        // documentation, not Vue components. Leaving HTML on makes the
        // Vue template compiler reject the unclosed tags.
        md.set({ html: false });
      },
    },
    mermaid: {
      theme: "neutral",
    },
  }),
);
