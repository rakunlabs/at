// Run with PLAYWRIGHT_MODULE pointing to an installed Playwright module.
// Mocked browser smoke, no running backend or provider credentials required.
import assert from 'node:assert/strict';
import { createServer } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { fileURLToPath } from 'node:url';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = fileURLToPath(new URL('..', import.meta.url));
const server = await createServer({ configFile: false, root, base: '/at/', cacheDir: '/tmp/opencode/personal-chat-vite-cache', plugins: [tailwindcss(), svelte()], resolve: { alias: { '@': `${root}/src` } }, server: { host: '127.0.0.1', port: 4187, strictPort: true, proxy: {}, watch: null }, logLevel: 'error' });
let browser;
const errors = [];
const event = (name, value) => `event: ${name}\ndata: ${JSON.stringify(value)}\n\n`;
const usage = { prompt_tokens: 0, completion_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, reasoning_tokens: 0, total_tokens: 0 };
try {
  await server.listen();
  browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox'] });
  for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
    const context = await browser.newContext({ viewport, serviceWorkers: 'block' });
    let enabled = true;
    let role = 'admin';
    let mode = 'completed';
    let record = { id: 'c1', owner_user_id: 'owner', title: 'Weekend planning', provider_key: 'openai', model: 'gpt-test', system_prompt: '', created_at: '2026-09-07T12:00:00Z', updated_at: '2026-09-07T12:00:00Z' };
    let messages = [];
    const writes = [];
    const reads = [];
    let uncertainID;
    await context.route('**/*', async route => {
      const request = route.request();
      const url = new URL(request.url());
      const path = url.pathname;
      const json = (data, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) });
      if (path === '/at/auth/status') return json({ enabled, passkeys: false });
      if (path === '/at/auth/me') return json({ subject: 'owner', name: 'Operator', roles: [role], claims: { session_id: 'family' } });
      if (!path.startsWith('/at/api/')) return route.continue();
      if (path === '/at/api/v1/info') return json({ name: 'AT', providers: [] });
      if (path === '/at/api/v1/features') return json({ groups: [], features: [] });
      if (!path.startsWith('/at/api/v1/conversations')) return json({ data: [], meta: { total: 0 } });
      assert.equal(enabled, true);
      assert.equal(role, 'admin');
      if (request.method() === 'GET') reads.push(path + url.search);
      else writes.push({ path, method: request.method(), body: request.postData() ? request.postDataJSON() : null });
      if (path.endsWith('/models')) return json([{ provider_key: 'openai', model: 'gpt-test' }, { provider_key: 'anthropic', model: 'claude-test' }]);
      if (path === '/at/api/v1/conversations') {
        if (request.method() === 'POST') { record = { ...record, ...request.postDataJSON() }; return json(record, 201); }
        return json({ items: [record], next_before: '' });
      }
      if (path === '/at/api/v1/conversations/c1') {
        if (request.method() === 'PATCH') { record = { ...record, ...request.postDataJSON() }; return json(record); }
        if (request.method() === 'DELETE') return route.fulfill({ status: 204 });
        return json(record);
      }
      if (path.endsWith('/cancel')) {
        assert.ok(path.endsWith(`/${messages.at(-1).id}/cancel`));
        messages[messages.length - 1] = { ...messages.at(-1), status: 'cancelled', error: 'generation_cancelled', content: 'Saved partial' };
        return json(messages.at(-1));
      }
      if (path.endsWith('/messages')) {
        if (request.method() === 'GET') return json({ items: [...messages].reverse(), next_before: '' });
        const body = request.postDataJSON();
        assert.deepEqual(Object.keys(body).sort(), ['content', 'request_id']);
        if (mode === 'uncertain') { uncertainID = body.request_id; mode = 'replay'; return route.abort('failed'); }
        const replay = mode === 'replay';
        if (replay) assert.equal(body.request_id, uncertainID);
        const base = { conversation_id: 'c1', request_id: body.request_id, provider_key: record.provider_key, model: record.model, finish_reason: '', error: '', usage, created_at: record.created_at };
        const user = { ...base, id: String(messages.length + 1).padStart(4, '0'), sequence: messages.length + 1, role: 'user', content: body.content, status: 'completed' };
        const assistant = { ...base, id: String(messages.length + 2).padStart(4, '0'), sequence: messages.length + 2, role: 'assistant', content: '', status: 'pending' };
        messages.push(user, assistant);
        let frames = event('accepted', { user, assistant, replay });
        if (mode === 'active' || replay) {
          frames += event('snapshot', assistant);
        } else {
          frames += event('delta', { assistant_message_id: assistant.id, offset: 0, content: 'é🙂' });
          frames += event('delta', { assistant_message_id: assistant.id, offset: 6, content: ' world' });
          messages[messages.length - 1] = { ...assistant, content: '**Saved answer** é🙂\n\n[Reference](https://example.org)\n\n<img src=x onerror=alert(1)>', status: 'completed', finish_reason: 'stop' };
          frames += event('done', messages.at(-1));
        }
        return route.fulfill({ contentType: 'text/event-stream', body: frames });
      }
      const found = messages.find(message => path.endsWith(`/${message.id}`));
      return found ? json(found) : json({ message: 'not found' }, 404);
    });
    const page = await context.newPage();
    page.on('pageerror', error => errors.push(error.message));
    page.setDefaultTimeout(15000);
    await page.goto('http://127.0.0.1:4187/at/#/chat');
    await page.getByRole('heading', { name: 'New conversation', exact: true }).waitFor();
    await page.getByLabel('Title', { exact: true }).fill('Weekend planning');
    await page.getByRole('button', { name: 'Create conversation', exact: true }).click();
    await page.getByLabel('Message', { exact: true }).waitFor();
    assert.equal(new URL(page.url()).hash, '#/chat/c1');
    await page.getByLabel('Message', { exact: true }).fill('Plan my weekend');
    await page.getByLabel('Message', { exact: true }).press('Shift+Enter');
    assert.equal(await page.getByLabel('Message', { exact: true }).inputValue(), 'Plan my weekend\n');
    await page.getByLabel('Message', { exact: true }).press('Enter');
    await page.getByText('Saved answer', { exact: false }).first().waitFor();
    assert.equal(await page.locator('article img').count(), 0);
    assert.equal(await page.getByRole('link', { name: 'Reference' }).getAttribute('rel'), 'noopener noreferrer');
    await page.getByLabel('Message', { exact: true }).fill('Unsent draft');
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await page.waitForTimeout(200);
    assert.equal(await page.getByLabel('Message', { exact: true }).inputValue(), 'Unsent draft');
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'no horizontal page overflow');
    const composerBox = await page.getByLabel('Message', { exact: true }).boundingBox();
    assert.ok(composerBox.y + composerBox.height <= viewport.height, 'composer remains in viewport');
    await page.screenshot({ path: `/tmp/opencode/personal-chat-${viewport.width}.png`, fullPage: true });

    mode = 'uncertain';
    await page.getByLabel('Message', { exact: true }).press('Enter');
    await page.getByRole('button', { name: 'Check / replay same request' }).waitFor();
    const sentCount = writes.filter(write => write.path.endsWith('/messages')).length;
    await page.reload();
    await page.getByRole('button', { name: 'Check / replay same request' }).waitFor();
    assert.equal(writes.filter(write => write.path.endsWith('/messages')).length, sentCount, 'reload does not resend unknown outcome');
    await page.getByRole('button', { name: 'Check / replay same request' }).click();
    await page.getByRole('button', { name: 'Stop answer' }).waitFor();
    await page.waitForTimeout(2200);
    assert.ok(reads.some(path => /\/messages\/0004$/.test(path)), 'active EOF polls assistant snapshot');
    await page.getByRole('button', { name: 'Stop answer' }).click();
    await page.getByText('Saved partial', { exact: true }).waitFor();
    await page.getByRole('button', { name: 'Settings', exact: true }).click();
    await page.getByLabel('Title', { exact: true }).fill('Renamed conversation');
    await page.getByRole('button', { name: 'Save changes' }).click();
    await page.getByRole('heading', { name: 'Renamed conversation' }).waitFor();
    await page.getByRole('button', { name: 'Settings', exact: true }).click();
    await page.getByRole('button', { name: 'Delete conversation', exact: true }).click();
    assert.equal(writes.filter(write => write.method === 'DELETE').length, 0);
    await page.getByRole('button', { name: 'Keep conversation' }).click();
    await page.getByRole('button', { name: 'Close settings' }).click();

    if (viewport.width < 768) {
      await page.getByRole('button', { name: 'Toggle conversation history' }).click();
      await page.getByRole('navigation', { name: 'Saved conversations' }).waitFor();
    }
    await page.goto('about:blank');
    enabled = false;
    await page.goto('http://127.0.0.1:4187/at/#/chat');
    await page.reload();
    await page.locator('textarea').first().waitFor();
    assert.equal(await page.getByRole('heading', { name: 'New conversation' }).count(), 0);
    await page.goto('about:blank');
    enabled = true;
    role = 'reader';
    await page.goto('http://127.0.0.1:4187/at/#/chat');
    await page.getByRole('heading', { name: 'Administrator access required' }).waitFor();
    console.log(`PASS ${viewport.width}px: create, keyboard send, snapshots, safe markdown, draft/focus, uncertain reload, explicit replay, active poll/cancel, rename, delete confirmation, native/legacy guards`);
    await context.close();
  }
  assert.deepEqual(errors, []);
} finally { await browser?.close(); await server.close(); }
