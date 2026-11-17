import { defineConfig } from 'vitepress'

// Published at https://pedro-stanaka.github.io/pmq/ (base path matters).
// Deployment: .github/workflows/docs.yml, from the default branch only.
const base = '/pmq/'

export default defineConfig({
  title: 'PMQ',
  description:
    "Poor Man's Quick — build it, drop it, run it. Self-hosted static app hosting with per-app JSON documents, object storage, and an OpenAI-compatible AI gateway.",
  base,
  cleanUrls: true,
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: `${base}favicon.svg` }]],
  themeConfig: {
    nav: [
      { text: 'Get Started', link: '/getting-started/' },
      { text: 'Concepts', link: '/concepts/architecture' },
      { text: 'Guides', link: '/guides/production' },
      { text: 'Reference', link: '/reference/api' },
      { text: 'For AI Agents', link: '/agents' },
      { text: 'GitHub', link: 'https://github.com/pedro-stanaka/pmq' },
    ],
    sidebar: [
      {
        text: 'Getting Started',
        items: [
          { text: 'Introduction', link: '/getting-started/' },
          { text: 'Install PMQ', link: '/getting-started/install' },
          { text: 'Deploy Your First App', link: '/getting-started/first-app' },
        ],
      },
      {
        text: 'Concepts',
        items: [
          { text: 'Architecture', link: '/concepts/architecture' },
          { text: 'Static Apps & Hostname Routing', link: '/concepts/apps' },
          { text: 'JSON Documents', link: '/concepts/documents' },
          { text: 'Object Storage', link: '/concepts/storage' },
          { text: 'AI Gateway', link: '/concepts/ai-gateway' },
          { text: 'Authentication & Trust Model', link: '/concepts/security' },
        ],
      },
      {
        text: 'Guides',
        items: [
          { text: 'Production & Recovery', link: '/guides/production' },
          { text: 'Caddy Integration', link: '/guides/caddy' },
          { text: 'Google SSO', link: '/guides/google-sso' },
          { text: 'Private Ingress', link: '/guides/private-ingress' },
          { text: 'Observability', link: '/guides/observability' },
          { text: 'AI Observability', link: '/guides/ai-observability' },
          { text: 'Upgrades & Rollback', link: '/guides/upgrades' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'CLI', link: '/reference/cli' },
          { text: 'Configuration', link: '/reference/config' },
          { text: 'HTTP API', link: '/reference/api' },
          { text: 'Browser SDK', link: '/reference/sdk' },
          { text: 'Releases & Distribution', link: '/reference/distribution' },
        ],
      },
      {
        text: 'Project',
        items: [
          { text: 'Roadmap & Limitations', link: '/project/' },
          { text: 'Developing PMQ', link: '/project/development' },
        ],
      },
    ],
    search: { provider: 'local' },
    socialLinks: [{ icon: 'github', link: 'https://github.com/pedro-stanaka/pmq' }],
    outline: 'deep',
    darkModeSwitchToggle: true,
    footer: {
      message: 'PMQ is a full-trust system for private environments.',
      copyright: 'MIT License — Pedro Tanaka',
    },
    docFooter: { prev: 'Previous', next: 'Next' },
  },
})
