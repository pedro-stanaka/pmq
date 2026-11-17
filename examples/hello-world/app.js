const form = document.querySelector('#form');
const error = document.querySelector('#error');
const notes = document.querySelector('#notes');
const fileForm = document.querySelector('#file-form');
const files = document.querySelector('#files');
const aiForm = document.querySelector('#ai-form');
const aiError = document.querySelector('#ai-error');
const answer = document.querySelector('#answer');
async function api(url, options) {
  const response = await fetch(url, options);
  const body = response.headers.get('Content-Type')?.includes('application/json') ? await response.json() : null;
  if (!response.ok) throw new Error(body.error?.message || `HTTP ${response.status}`);
  return body;
}
function objectURL(name) {
  return `/_pmq/storage/${encodeURIComponent(name)}`;
}
async function refresh() {
  try {
    const result = await api('/_pmq/db/notes');
    notes.replaceChildren(...result.documents.map(doc => {
      const item = document.createElement('li');
      item.textContent = doc.data.text;
      return item;
    }));
    error.textContent = '';
  } catch (e) { error.textContent = e.message; }
}
async function refreshFiles() {
  try {
    const result = await api('/_pmq/storage?limit=100');
    files.replaceChildren(...result.objects.map(object => {
      const item = document.createElement('li');
      const download = document.createElement('a');
      download.href = objectURL(object.key);
      download.textContent = `${object.key} (${object.size} bytes)`;
      download.download = object.key.split('/').pop();
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.textContent = 'Delete';
      remove.addEventListener('click', async () => {
        try {
          await api(objectURL(object.key), {method: 'DELETE'});
          await refreshFiles();
        } catch (e) { error.textContent = e.message; }
      });
      item.append(download, ' ', remove);
      return item;
    }));
    error.textContent = '';
  } catch (e) { error.textContent = e.message; }
}
form.addEventListener('submit', async event => {
  event.preventDefault();
  try {
    await api('/_pmq/db/notes', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({text: document.querySelector('#note').value})});
    form.reset();
    await refresh();
  } catch (e) { error.textContent = e.message; }
});
fileForm.addEventListener('submit', async event => {
  event.preventDefault();
  const file = document.querySelector('#file').files[0];
  if (!file) return;
  try {
    await api(objectURL(file.name), {method: 'PUT', headers: {'Content-Type': file.type || 'application/octet-stream'}, body: file});
    fileForm.reset();
    await refreshFiles();
  } catch (e) { error.textContent = e.message; }
});
aiForm.addEventListener('submit', async event => {
  event.preventDefault();
  answer.textContent = '';
  try {
    const result = await api('/_pmq/ai/chat/completions', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({model: document.querySelector('#model').value, messages: [{role: 'user', content: document.querySelector('#prompt').value}]}),
    });
    answer.textContent = result.choices?.[0]?.message?.content || '';
    aiError.textContent = '';
  } catch (e) { aiError.textContent = e.message; }
});
refresh();
refreshFiles();
