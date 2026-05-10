import { defineConfig } from 'vitepress'

// https://vitepress.dev/reference/site-config
export default defineConfig({
  title: 'pika',
  description: 'General configuration server, secrets manager, and raw file server',
  cleanUrls: true,
  lastUpdated: true,
  ignoreDeadLinks: 'localhostLinks',

  themeConfig: {
    logo: '/favicon-192x192.png',

    nav: [
      { text: 'Guide', link: '/guide/getting-started' },
      { text: 'Reference', link: '/reference/consuming-data' },
      {
        text: 'Links',
        items: [
          { text: 'GitHub', link: 'https://github.com/rakunlabs/pika' },
          { text: 'Container image', link: 'https://github.com/rakunlabs/pika/pkgs/container/pika' },
        ],
      },
    ],

    sidebar: {
      '/guide/': [
        {
          text: 'Getting started',
          items: [
            { text: 'Introduction', link: '/guide/getting-started' },
            { text: 'Installation', link: '/guide/installation' },
            { text: 'Configuration', link: '/guide/configuration' },
          ],
        },
        {
          text: 'Configs',
          items: [
            { text: 'Concepts', link: '/guide/concepts' },
            { text: 'Versions & variants', link: '/guide/versions-variants' },
            { text: 'Inheritance', link: '/guide/inheritance' },
            { text: 'Kubernetes resource', link: '/guide/kubernetes-external' },
          ],
        },
        {
          text: 'Files & events',
          items: [
            { text: 'Raw file serving', link: '/guide/raw-files' },
            { text: 'Hooks', link: '/guide/hooks' },
          ],
        },
        {
          text: 'Operations',
          items: [
            { text: 'Authentication', link: '/guide/authentication' },
            { text: 'Encryption', link: '/guide/encryption' },
            { text: 'Clustering', link: '/guide/clustering' },
            { text: 'Kubernetes', link: '/guide/kubernetes' },
          ],
        },
      ],

      '/reference/': [
        {
          text: 'Reference',
          items: [
            { text: 'Consuming data', link: '/reference/consuming-data' },
            { text: 'Admin API', link: '/reference/api' },
            { text: 'Tokens & scopes', link: '/reference/tokens-and-scopes' },
            { text: 'Compat endpoints', link: '/reference/compat' },
          ],
        },
      ],
    },

    socialLinks: [
      { icon: 'github', link: 'https://github.com/rakunlabs/pika' },
    ],

    editLink: {
      pattern: 'https://github.com/rakunlabs/pika/edit/main/_docs/:path',
      text: 'Edit this page on GitHub',
    },

    search: {
      provider: 'local',
    },

    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © Rakun Labs',
    },
  },
})
