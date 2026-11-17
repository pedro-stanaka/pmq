import { createPMQClient } from '@pmq-dev/client';
import './style.css';

const pmq = createPMQClient();
const output = document.querySelector<HTMLPreElement>('#output')!;

async function refresh() {
  const page = await pmq.documents.list('notes');
  output.textContent = JSON.stringify(page.documents.map(({ id, data }) => ({ id, ...data })), null, 2);
}

document.querySelector<HTMLFormElement>('#note-form')!.addEventListener('submit', async (event) => {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  try {
    await pmq.documents.create('notes', { title: String(form.get('title')), createdAt: new Date().toISOString() });
    await refresh();
  } catch (error) {
    output.textContent = error instanceof Error ? error.message : String(error);
  }
});

document.querySelector<HTMLButtonElement>('#ask-ai')!.addEventListener('click', async () => {
  try {
    const response = await pmq.ai.complete({ model: 'fast', messages: [{ role: 'user', content: 'Say hello in one short sentence.' }] });
    output.textContent = response.choices[0]?.message.content ?? 'No text response';
  } catch (error) {
    output.textContent = error instanceof Error ? error.message : String(error);
  }
});

void refresh().catch((error: unknown) => { output.textContent = error instanceof Error ? error.message : String(error); });
