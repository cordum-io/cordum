import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'Cordum Docs',
  tagline: 'Safety-first agent orchestration platform',
  favicon: 'img/favicon.ico',

  future: {
    v4: true,
  },

  markdown: {
    format: 'detect',
  },

  url: 'https://docs.cordum.io',
  baseUrl: '/',

  organizationName: 'cordum-io',
  projectName: 'cordum',

  onBrokenLinks: 'warn',
  onBrokenMarkdownLinks: 'warn',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          routeBasePath: '/',
          editUrl: 'https://github.com/cordum-io/cordum/tree/main/docs-site/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  plugins: [
    [
      '@docusaurus/plugin-client-redirects',
      {
        redirects: [
          // Legacy URLs referenced in codebase (GOVERNANCE.md, dashboard settings)
          {from: '/api', to: '/api-reference/full-reference'},
          {from: '/configuration', to: '/operations/configuration-guide'},
          {from: '/deployment', to: '/operations/deployment'},
          {from: '/quickstart', to: '/'},
          {from: '/architecture', to: '/concepts/architecture'},
          {from: '/safety-kernel', to: '/concepts/safety-kernel'},
        ],
      },
    ],
  ],

  themes: [
    [
      '@easyops-cn/docusaurus-search-local',
      {
        hashed: true,
        language: ['en'],
        indexBlog: false,
        docsRouteBasePath: '/',
      },
    ],
  ],

  themeConfig: {
    image: 'img/cordum-social-card.png',
    colorMode: {
      defaultMode: 'dark',
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'Cordum Docs',
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'gettingStarted',
          position: 'left',
          label: 'Getting Started',
        },
        {
          type: 'docSidebar',
          sidebarId: 'concepts',
          position: 'left',
          label: 'Concepts',
        },
        {
          type: 'docSidebar',
          sidebarId: 'tutorials',
          position: 'left',
          label: 'Tutorials',
        },
        {
          type: 'docSidebar',
          sidebarId: 'apiReference',
          position: 'left',
          label: 'API Reference',
        },
        {
          type: 'docSidebar',
          sidebarId: 'operations',
          position: 'left',
          label: 'Operations',
        },
        {
          type: 'docsVersionDropdown',
          position: 'right',
        },
        {
          href: 'https://cordum.io',
          label: 'cordum.io',
          position: 'right',
        },
        {
          href: 'https://github.com/cordum-io/cordum',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {label: 'Getting Started', to: '/'},
            {label: 'API Reference', to: '/api-reference/full-reference'},
            {label: 'Operations', to: '/operations/deployment'},
          ],
        },
        {
          title: 'Product',
          items: [
            {label: 'Website', href: 'https://cordum.io'},
            {label: 'GitHub', href: 'https://github.com/cordum-io/cordum'},
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Cordum. Built with Docusaurus.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'go', 'yaml', 'json', 'protobuf', 'toml'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
