const collection = '/_pmq/db/tasks';
const form = document.querySelector('#task-form');
const input = document.querySelector('#task-text');
const list = document.querySelector('#tasks');
const status = document.querySelector('#status');

async function request(path, options) {
  const response = await fetch(path, options);
  const text = await response.text();
  const body = text ? JSON.parse(text) : null;
  if (!response.ok) {
    throw new Error(body?.error?.message || `Request failed (${response.status})`);
  }
  return body;
}

async function refresh() {
  status.textContent = 'Loading tasks…';
  try {
    const result = await request(collection);
    render(result.documents);
    status.textContent = result.documents.length ? '' : 'No tasks yet. Add one above.';
  } catch (error) {
    status.textContent = `${error.message}. Retry by reloading the page.`;
  }
}

function render(documents) {
  list.replaceChildren(...documents.map((doc) => {
    const item = document.createElement('li');
    const label = document.createElement('label');
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.checked = Boolean(doc.data.done);
    checkbox.setAttribute('aria-label', `Mark ${doc.data.text} complete`);
    checkbox.addEventListener('change', () => update(doc, {done: checkbox.checked, text: doc.data.text}));

    const text = document.createElement('span');
    text.textContent = doc.data.text;
    if (doc.data.done) text.className = 'done';
    label.append(checkbox, text);

    const remove = document.createElement('button');
    remove.type = 'button';
    remove.textContent = 'Delete';
    remove.setAttribute('aria-label', `Delete ${doc.data.text}`);
    remove.addEventListener('click', () => erase(doc.id));
    item.append(label, remove);
    return item;
  }));
}

async function update(doc, data) {
  try {
    await request(`${collection}/${encodeURIComponent(doc.id)}`, {
      method: 'PUT',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(data),
    });
    await refresh();
  } catch (error) {
    status.textContent = `Could not update task: ${error.message}`;
    await refresh();
  }
}

async function erase(id) {
  try {
    await request(`${collection}/${encodeURIComponent(id)}`, {method: 'DELETE'});
    await refresh();
  } catch (error) {
    status.textContent = `Could not delete task: ${error.message}`;
  }
}

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  const text = input.value.trim();
  if (!text) return;
  const button = form.querySelector('button');
  button.disabled = true;
  status.textContent = 'Saving task…';
  try {
    await request(collection, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({text, done: false}),
    });
    form.reset();
    await refresh();
    input.focus();
  } catch (error) {
    status.textContent = `Could not save task: ${error.message}`;
  } finally {
    button.disabled = false;
  }
});

refresh();
