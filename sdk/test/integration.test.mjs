import assert from 'node:assert/strict';
import { spawn, spawnSync, execFileSync } from 'node:child_process';
import { createServer } from 'node:http';
import { createServer as createNetServer } from 'node:net';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { after, before, test } from 'node:test';
import { createPMQClient, PMQError } from '../dist/index.js';

const repo = resolve(new URL('../..', import.meta.url).pathname);
const temp = mkdtempSync(join(tmpdir(), 'pmq-sdk-'));
const dataDir = join(temp, 'data');
let pmqProcess;
let aiServer;
let client;
let baseUrl;
let requestedCredentials = [];

async function freePort() {
  const server = createNetServer();
  await new Promise((resolveListen, reject) => {
    server.once('error', reject);
    server.listen(0, '::1', resolveListen);
  });
  const { port } = server.address();
  await new Promise((resolveClose) => server.close(resolveClose));
  return port;
}

async function listen(server) {
  await new Promise((resolveListen, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolveListen);
  });
  return server.address().port;
}

before(async () => {
  aiServer = createServer(async (req, res) => {
    let raw = '';
    for await (const part of req) raw += part;
    const input = JSON.parse(raw);
    if (input.messages?.[0]?.content === 'slow') {
      await delay(500);
    }
    if (res.destroyed) return;
    if (input.stream) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      const base = { id: 'chatcmpl-sdk-test', object: 'chat.completion.chunk', created: 1, model: input.model };
      for (const [content, finish_reason] of [['hello from ', null], ['test provider', null], ['', 'stop']]) {
        res.write(`data: ${JSON.stringify({ ...base, choices: [{ index: 0, delta: content ? { content } : {}, finish_reason }] })}\n\n`);
      }
      res.end('data: [DONE]\n\n');
      return;
    }
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      id: 'chatcmpl-sdk-test',
      object: 'chat.completion',
      created: 1,
      model: input.model,
      choices: [{ index: 0, message: { role: 'assistant', content: 'hello from test provider' }, finish_reason: 'stop' }],
      usage: { prompt_tokens: 1, completion_tokens: 4, total_tokens: 5 },
    }));
  });
  const aiPort = await listen(aiServer);
  const port = await freePort();
  baseUrl = `http://sdk-test.apps.localhost:${port}`;
  const binary = join(temp, 'pmq');
  execFileSync('go', ['build', '-o', binary, './cmd/pmq'], { cwd: repo, stdio: 'inherit' });
  pmqProcess = spawn(binary, [
    'serve', '--dev', '--listen', `[::1]:${port}`, '--data-dir', dataDir,
    '--domain', 'apps.localhost', '--token', 'integration-test-token',
  ], {
    cwd: repo,
    env: {
      ...process.env,
      PMQ_AI_PROVIDER: 'openai',
      PMQ_AI_MODELS: 'fast=test-model',
      PMQ_OPENAI_BASE_URL: `http://127.0.0.1:${aiPort}/v1`,
      OPENAI_API_KEY: 'integration-test-key',
    },
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let stderr = '';
  pmqProcess.stderr.setEncoding('utf8');
  pmqProcess.stderr.on('data', (chunk) => { stderr += chunk; });
  let healthy = false;
  for (let i = 0; i < 100; i++) {
    if (pmqProcess.exitCode !== null) throw new Error(`PMQ exited during startup: ${stderr}`);
    try {
      const response = await fetch(`http://[::1]:${port}/_pmq/health`);
      if (response.ok) { healthy = true; break; }
    } catch { /* Wait for the listener to start. */ }
    await delay(50);
  }
  if (!healthy) throw new Error(`PMQ did not become healthy: ${stderr}`);
  execFileSync(binary, [
    'deploy', resolve(repo, 'examples/hello-world'), '--name', 'sdk-test',
    '--server', `http://[::1]:${port}`, '--token', 'integration-test-token',
  ], { cwd: repo, stdio: 'inherit' });

  const browserFetch = async (input, init = {}) => {
    requestedCredentials.push(init.credentials);
    const url = new URL(input, baseUrl);
    const headers = new Headers(init.headers);
    const method = (init.method ?? 'GET').toUpperCase();
    if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) headers.set('Origin', url.origin);
    return fetch(url, { ...init, headers });
  };
  client = createPMQClient({ baseUrl, fetch: browserFetch });
});

after(async () => {
  if (pmqProcess && pmqProcess.exitCode === null) {
    pmqProcess.kill('SIGTERM');
    await Promise.race([
      new Promise((resolveExit) => pmqProcess.once('exit', resolveExit)),
      delay(3000).then(() => pmqProcess.kill('SIGKILL')),
    ]);
  }
  if (aiServer) await new Promise((resolveClose) => aiServer.close(resolveClose));
  rmSync(temp, { recursive: true, force: true });
});

test('document CRUD exercises the real PMQ API', async () => {
  const created = await client.documents.create('notes', { title: 'first', done: false });
  assert.match(created.id, /^[a-f0-9]{32}$/);
  assert.equal(created.collection, 'notes');
  assert.deepEqual(created.data, { title: 'first', done: false });

  const listed = await client.documents.list('notes', { limit: 10, offset: 0 });
  assert.equal(listed.documents.length, 1);
  assert.equal(listed.documents[0].id, created.id);

  const loaded = await client.documents.get('notes', created.id);
  assert.equal(loaded.data.title, 'first');
  const updated = await client.documents.update('notes', created.id, { title: 'updated' });
  assert.deepEqual(updated.data, { title: 'updated' });
  await client.documents.delete('notes', created.id);

  await assert.rejects(client.documents.get('notes', '00000000000000000000000000000000'), (error) => {
    assert.ok(error instanceof PMQError);
    assert.equal(error.status, 404);
    assert.equal(error.code, 'document_not_found');
    return true;
  });
});

test('storage upload, metadata, download, list and delete use actual storage endpoints', async () => {
  await client.storage.upload('sdk/example.txt', new Blob(['stored bytes'], { type: 'text/plain' }));
  const metadata = await client.storage.head('sdk/example.txt');
  assert.equal(metadata.key, 'sdk/example.txt');
  assert.equal(metadata.size, 12);
  assert.match(metadata.contentType ?? '', /^text\/plain/);
  const downloaded = await client.storage.get('sdk/example.txt');
  assert.equal(await downloaded.text(), 'stored bytes');
  const page = await client.storage.list({ prefix: 'sdk/', limit: 10 });
  assert.equal(page.objects[0].key, 'sdk/example.txt');
  await client.storage.delete('sdk/example.txt');
});

test('storage HEAD maps metadata headers', async () => {
  const fixture = createPMQClient({ fetch: async (_input, init) => {
    assert.equal(init.method, 'HEAD');
    return new Response(null, { headers: { 'Content-Length': '12', 'Content-Type': 'text/plain', 'Last-Modified': 'Tue, 17 Nov 2026 12:00:00 GMT', ETag: '"v1"' } });
  } });
  const metadata = await fixture.storage.head('a.txt');
  assert.deepEqual(metadata, { key: 'a.txt', size: 12, lastModified: 'Tue, 17 Nov 2026 12:00:00 GMT', contentType: 'text/plain', etag: '"v1"' });
});

test('non-streaming AI completion uses the real PMQ gateway endpoint', async () => {
  const completion = await client.ai.complete({ model: 'fast', messages: [{ role: 'user', content: 'hello' }] });
  assert.equal(completion.object, 'chat.completion');
  assert.equal(completion.model, 'fast');
  assert.equal(completion.choices[0].message.content, 'hello from test provider');
});

test('AI streaming uses the real PMQ gateway when the server supports the #6 streaming contract', async (t) => {
  try {
    const chunks = [];
    for await (const chunk of client.ai.stream({ model: 'fast', messages: [{ role: 'user', content: 'hello' }] })) chunks.push(chunk);
    assert.equal(chunks.filter((event) => event.type === 'chunk').at(-1).chunk.choices[0].finish_reason, 'stop');
    assert.equal(chunks.map((event) => event.type === 'chunk' ? event.chunk.choices[0].delta.content ?? '' : '').join(''), 'hello from test provider');
  } catch (error) {
    if (error instanceof PMQError && error.status === 400) {
      t.skip('this worktree predates the #6 streaming server implementation; rerun after stacking #6');
      return;
    }
    throw error;
  }
});

test('AI stream yields incrementally, preserves typed errors and cancels on early exit', async () => {
  const frames = [
    'data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"fast","choices":[{"index":0,"delta":{"content":"one"},"finish_reason":null}]}\r\n\r\n',
    'data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"fast","choices":[{"index":0,"delta":{"content":"two"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n',
  ];
  let reads = 0;
  let canceled = false;
  const client = createPMQClient({ fetch: async (_input, init) => {
    assert.equal(init.credentials, 'same-origin');
    assert.equal(JSON.parse(init.body).stream, true);
    return new Response(new ReadableStream({
      pull(controller) { reads++; if (frames.length) controller.enqueue(new TextEncoder().encode(frames.shift())); else controller.close(); },
      cancel() { canceled = true; },
    }, { highWaterMark: 0 }), { headers: { 'Content-Type': 'text/event-stream' } });
  } });
  const iterator = client.ai.stream({ model: 'fast', messages: [{ role: 'user', content: 'x' }] })[Symbol.asyncIterator]();
  const first = await iterator.next();
  assert.equal(first.value.type, 'chunk');
  assert.equal(first.value.chunk.choices[0].delta.content, 'one');
  assert.equal(reads, 1, 'a consumer pause must not pull another response chunk');
  await iterator.return();
  assert.equal(canceled, true, 'breaking iteration must cancel the response body');

  const controller = new AbortController();
  const abortClient = createPMQClient({ fetch: async (_input, init) => new Promise((_resolve, reject) => {
    assert.equal(init.signal, controller.signal);
    const abort = () => reject(new DOMException('Aborted', 'AbortError'));
    if (init.signal.aborted) abort();
    else init.signal.addEventListener('abort', abort, { once: true });
  }) });
  const pending = abortClient.ai.stream({ model: 'fast', messages: [{ role: 'user', content: 'x' }] }, { signal: controller.signal })[Symbol.asyncIterator]().next();
  controller.abort();
  await assert.rejects(pending, (error) => error.name === 'AbortError');

  const errorClient = createPMQClient({ fetch: async () => new Response('event: error\ndata: {"error":{"code":"provider_error","message":"stream failed"}}\n\n', { headers: { 'Content-Type': 'text/event-stream' } }) });
  await assert.rejects(async () => { for await (const _chunk of errorClient.ai.stream({ model: 'fast', messages: [{ role: 'user', content: 'x' }] })) {} }, (error) => {
    assert.ok(error instanceof PMQError);
    assert.equal(error.code, 'provider_error');
    return true;
  });

  const usageClient = createPMQClient({ fetch: async () => new Response('event: future-extension\ndata: {"x":1}\n\nevent: pmq-usage\ndata: {"status":"reported","usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5},"estimated_cost_usd":0.0001,"cost_basis":"reported_tokens_and_configured_price"}\n\ndata: [DONE]\n\n', { headers: { 'Content-Type': 'text/event-stream' } }) });
  const events = [];
  for await (const event of usageClient.ai.stream({ model: 'fast', messages: [{ role: 'user', content: 'x' }] })) events.push(event);
  assert.deepEqual(events, [{ type: 'usage', status: 'reported', usage: { prompt_tokens: 2, completion_tokens: 3, total_tokens: 5 }, estimated_cost_usd: 0.0001, cost_basis: 'reported_tokens_and_configured_price' }]);
});

test('typed API errors and AbortSignal cancellation are preserved', async () => {
  await assert.rejects(client.documents.list('invalid.collection'), (error) => {
    assert.ok(error instanceof PMQError);
    assert.equal(error.status, 400);
    assert.equal(error.code, 'invalid_path');
    return true;
  });

  const controller = new AbortController();
  const pending = client.ai.complete({ model: 'fast', messages: [{ role: 'user', content: 'slow' }] }, { signal: controller.signal });
  setTimeout(() => controller.abort(), 20);
  await assert.rejects(pending, (error) => error.name === 'AbortError');
});

test('requests explicitly use same-origin credentials', async () => {
  assert.ok(requestedCredentials.length > 0);
  assert.ok(requestedCredentials.every((mode) => mode === 'same-origin'));
});
