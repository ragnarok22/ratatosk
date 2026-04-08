import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Ratatosk',
  description: 'A fast, self-hosted ngrok alternative',
  base: '/ratatosk/',

  lastUpdated: true,

  head: [['link', { rel: 'icon', href: '/ratatosk/ratatosk-logo.png' }]],

  themeConfig: {
    logo: '/ratatosk-logo.png',

    nav: [
      { text: 'Guide', link: '/guide/introduction' },
      { text: 'Reference', link: '/reference/cli-commands' },
    ],

    sidebar: {
      '/guide/': [
        {
          text: 'Guide',
          items: [
            { text: 'Introduction', link: '/guide/introduction' },
            { text: 'Installation', link: '/guide/installation' },
            { text: 'Getting Started', link: '/guide/getting-started' },
            { text: 'Deployment', link: '/guide/deployment' },
          ],
        },
      ],
      '/reference/': [
        {
          text: 'Reference',
          items: [
            { text: 'CLI Commands', link: '/reference/cli-commands' },
            { text: 'Configuration', link: '/reference/configuration' },
            { text: 'Architecture', link: '/reference/architecture' },
          ],
        },
      ],
    },

    socialLinks: [
      { icon: 'github', link: 'https://github.com/ragnarok22/ratatosk' },
    ],

    editLink: {
      pattern:
        'https://github.com/ragnarok22/ratatosk/edit/main/docs/:path',
      text: 'Edit this page on GitHub',
    },

    footer: {
      message: 'Released under the GPLv3 License.',
      copyright: 'Copyright 2025-present Ratatosk Contributors',
    },

    search: {
      provider: 'local',
    },
  },
})
