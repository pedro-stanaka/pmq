---
layout: home
markdownStyles: false
---

<script setup>
import { withBase } from 'vitepress'
import { ref } from 'vue'
const copied = ref(false)
const deployCommand = 'pmq deploy ./examples/hello-world --name hello-world --server http://127.0.0.1:8080'
async function copyCommand() {
  try { await navigator.clipboard.writeText(deployCommand); copied.value = true } catch { copied.value = false }
}
</script>

<div class="home-shell">
  <section class="hero-layout">
    <div class="hero-copy">
      <div class="hero-mark" aria-hidden="true"><span></span><span></span><span></span></div>
      <h1>Your tiny app deserves a tiny cloud.</h1>
      <p class="hero-intro">PMQ gives your static apps a home, persistent data, and an AI gateway. One small server, on infrastructure you already own.</p>
      <div class="hero-actions">
        <a class="action primary" :href="withBase('/getting-started/')">Get started <span aria-hidden="true">→</span></a>
        <a class="action secondary" href="https://github.com/pedro-stanaka/pmq">View on GitHub <span aria-hidden="true">↗</span></a>
      </div>
      <p class="hero-signoff">Build it. Drop it. Run it.</p>
    </div>
    <div class="terminal" aria-label="Example deployment command">
      <div class="terminal-top"><span class="terminal-dots" aria-hidden="true"><i></i><i></i><i></i></span><span>terminal</span><span>PMQ / deploy</span></div>
      <div class="terminal-body">
        <p class="terminal-comment"># From the PMQ repository, with the server running</p>
        <div class="language-sh"><button class="copy-command" type="button" @click="copyCommand">{{ copied ? 'Copied' : 'Copy' }}</button><pre><code>{{ deployCommand }}</code></pre></div>
        <p class="terminal-note">A built directory in. Your app on its own hostname.</p>
      </div>
      <div class="terminal-foot"><span>STATIC APPS</span><span>ONE GO SERVER</span></div>
    </div>
  </section>

  <section class="home-section architecture">
    <div><h2>Less infrastructure.<br>More making.</h2><p>Ship a static frontend without a new container, database, or deployment pipeline for every idea.</p><a :href="withBase('/concepts/architecture')">Explore the architecture →</a></div>
    <div class="flow" aria-label="Architecture: trusted users connect through a reverse proxy to PMQ, which hosts apps and services">
      <div><small>01 / ACCESS</small><strong>Trusted users</strong><span>Reverse proxy + authentication</span></div><div><small>02 / HOST</small><strong>PMQ server</strong><span>One self-hosted Go process</span></div><div><small>03 / BUILD</small><strong>Your apps</strong><span>Static files, data, storage + AI</span></div>
    </div>
  </section>

  <section class="home-section capabilities">
    <div class="section-heading"><h2>The essentials, built in.</h2><p>Everything a small app needs to get out of your head and onto a hostname.</p></div>
    <div class="capability-grid">
      <article><span>01</span><h3>Static hosting</h3><p>Deploy a directory or zip. Releases activate atomically and can be rolled back.</p></article>
      <article><span>02</span><h3>JSON documents</h3><p>Per-app collections backed by SQLite, available over a simple HTTP API.</p></article>
      <article><span>03</span><h3>Object storage</h3><p>Store files on the local filesystem or an S3-compatible bucket.</p></article>
      <article><span>04</span><h3>AI gateway</h3><p>Use an optional OpenAI-compatible text API while provider keys stay server-side.</p></article>
      <article><span>05</span><h3>Shared access</h3><p>Use your existing trusted reverse proxy for authentication across apps.</p></article>
      <article><span>06</span><h3>Made for agents</h3><p>Predictable commands and APIs give coding agents a straightforward path to deploy.</p></article>
    </div>
  </section>
  <section class="closing"><div><h2>Make something small.</h2><p>PMQ is for private, full-trust environments. It is not designed for hostile multi-tenancy.</p></div><a class="action primary" :href="withBase('/getting-started/')">Read the docs <span aria-hidden="true">→</span></a></section>
</div>
