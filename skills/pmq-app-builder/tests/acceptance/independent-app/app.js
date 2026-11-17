const endpoint = '/_pmq/db/contacts';
const form = document.querySelector('#contact-form');
const contacts = document.querySelector('#contacts');
const status = document.querySelector('#status');

async function send(path, options) {
  const response = await fetch(path, options);
  const text = await response.text();
  const body = text ? JSON.parse(text) : null;
  if (!response.ok) throw new Error(body?.error?.message || `HTTP ${response.status}`);
  return body;
}

async function loadContacts() {
  try {
    const result = await send(endpoint);
    contacts.replaceChildren(...result.documents.map((doc) => {
      const item = document.createElement('li');
      item.textContent = `${doc.data.name} — ${doc.data.email}`;
      return item;
    }));
    status.textContent = result.documents.length ? '' : 'No contacts saved.';
  } catch (error) {
    status.textContent = `Could not load contacts: ${error.message}`;
  }
}

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = form.querySelector('button');
  button.disabled = true;
  status.textContent = 'Saving…';
  try {
    await send(endpoint, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({
        name: document.querySelector('#contact-name').value.trim(),
        email: document.querySelector('#contact-email').value.trim(),
      }),
    });
    form.reset();
    await loadContacts();
  } catch (error) {
    status.textContent = `Could not save contact: ${error.message}`;
  } finally {
    button.disabled = false;
  }
});

loadContacts();
