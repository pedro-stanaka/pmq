const form = document.querySelector('#form');
const error = document.querySelector('#error');
const notes = document.querySelector('#notes');
async function api(url, options) {
  const response = await fetch(url, options);
  const body = await response.json();
  if (!response.ok) throw new Error(body.error?.message || `HTTP ${response.status}`);
  return body;
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
form.addEventListener('submit', async event => {
  event.preventDefault();
  try {
    await api('/_pmq/db/notes', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({text: document.querySelector('#note').value})});
    form.reset();
    await refresh();
  } catch (e) { error.textContent = e.message; }
});
refresh();
