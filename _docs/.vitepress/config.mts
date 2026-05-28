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
    logo: '/logo.svg',
    siteTitle: 'pika',

    nav: [
      { text: 'Guide', link: '/guide/getting-started' },
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
            { text: 'Consuming data', link: '/guide/consuming-data' },
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
                { text: 'GCP Secret Manager', link: '/guide/inheritance/gcp' },
                { text: 'GCP Parameter Manager', link: '/guide/inheritance/gcp-parameter' },
                { text: 'Azure', link: '/guide/inheritance/azure' },
              ],
            },
          ],
        },
        {
          text: 'Serving',
          items: [
            { text: 'Endpoints', link: '/guide/endpoints' },
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
            { text: 'Tokens & scopes', link: '/guide/tokens-and-scopes' },
            { text: 'Encryption', link: '/guide/encryption' },
            { text: 'Server key management', link: '/guide/server-key-management' },
            { text: 'Clustering', link: '/guide/clustering' },
            { text: 'Kubernetes', link: '/guide/kubernetes' },
          ],
        },
        {
          text: 'API',
          items: [
            { text: 'Admin API', link: '/guide/api' },
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
