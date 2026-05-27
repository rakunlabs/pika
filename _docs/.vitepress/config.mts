import { defineConfig } from 'vitepress'

// https://vitepress.dev/reference/site-config
export default defineConfig({
  title: 'pika',
  description: 'General configuration server, secrets manager, and personal vault',
  base: '/pika/',
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
            {
              text: 'Inheritance',
              link: '/guide/inheritance/',
              collapsed: true,
              items: [
                { text: 'HTTP', link: '/guide/inheritance/http' },
                { text: 'Vault', link: '/guide/inheritance/vault' },
                { text: 'Kubernetes', link: '/guide/inheritance/kubernetes' },
                { text: 'Consul', link: '/guide/inheritance/consul' },
                { text: 'etcd', link: '/guide/inheritance/etcd' },
                { text: 'AWS', link: '/guide/inheritance/aws' },
                { text: 'GCP', link: '/guide/inheritance/gcp' },
                { text: 'Azure', link: '/guide/inheritance/azure' },
              ],
            },
          ],
        },
        {
          text: 'Events',
          items: [
            { text: 'Hooks', link: '/guide/hooks' },
          ],
        },
        {
          text: 'Personal vault',
          items: [
            { text: 'Overview', link: '/guide/vault' },
          ],
        },
        {
          text: 'Operations',
          items: [
            { text: 'Authentication', link: '/guide/authentication' },
            { text: 'Encryption', link: '/guide/encryption' },
            { text: 'Server key management', link: '/guide/server-key-management' },
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
            { text: 'Endpoints', link: '/reference/compat' },
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
