import {useEffect, useState, type FormEvent} from 'react';

const collection = '/_pmq/db/tasks';

type Task = {
  id: string;
  data: {text: string; done: boolean};
};

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, options);
  const text = await response.text();
  const body = text ? JSON.parse(text) as unknown : null;
  if (!response.ok) {
    const message = (body as {error?: {message?: string}} | null)?.error?.message;
    throw new Error(message || `Request failed (${response.status})`);
  }
  return body as T;
}

export default function App() {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [draft, setDraft] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  async function refresh() {
    setLoading(true);
    try {
      const result = await request<{documents: Task[]}>(collection);
      setTasks(result.documents);
      setError('');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not load tasks');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { void refresh(); }, []);

  async function addTask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const text = draft.trim();
    if (!text) return;
    setSaving(true);
    setError('');
    try {
      await request<Task>(collection, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({text, done: false}),
      });
      setDraft('');
      await refresh();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not save task');
    } finally {
      setSaving(false);
    }
  }

  async function changeTask(task: Task, done: boolean) {
    setError('');
    try {
      await request<Task>(`${collection}/${encodeURIComponent(task.id)}`, {
        method: 'PUT',
        headers: {'Content-Type': 'application/json'},
        // PUT replaces the entire data object; preserve every app-owned field.
        body: JSON.stringify({...task.data, done}),
      });
      await refresh();
    } catch (cause) {
      await refresh();
      setError(cause instanceof Error ? cause.message : 'Could not update task');
    }
  }

  async function deleteTask(id: string) {
    setError('');
    try {
      await request<void>(`${collection}/${encodeURIComponent(id)}`, {method: 'DELETE'});
      await refresh();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not delete task');
    }
  }

  return (
    <main className="shell">
      <header>
        <p className="eyebrow">A small PMQ example</p>
        <h1>Task list</h1>
        <p>Tasks are stored for this app and shared with its trusted users.</p>
      </header>

      <form onSubmit={addTask}>
        <label htmlFor="task-text">New task</label>
        <div className="form-row">
          <input id="task-text" value={draft} onChange={(event) => setDraft(event.target.value)} maxLength={200} required />
          <button disabled={saving}>{saving ? 'Saving…' : 'Add task'}</button>
        </div>
      </form>

      {error && <p className="error" role="alert">{error}</p>}
      <p className="status" role="status" aria-live="polite">
        {loading ? 'Loading tasks…' : tasks.length === 0 ? 'No tasks yet. Add one above.' : `${tasks.length} task${tasks.length === 1 ? '' : 's'}`}
      </p>
      <ul aria-label="Tasks">
        {tasks.map((task) => (
          <li key={task.id}>
            <label>
              <input type="checkbox" checked={Boolean(task.data.done)} onChange={(event) => void changeTask(task, event.target.checked)} />
              <span className={task.data.done ? 'done' : ''}>{task.data.text}</span>
            </label>
            <button className="delete" type="button" onClick={() => void deleteTask(task.id)} aria-label={`Delete ${task.data.text}`}>Delete</button>
          </li>
        ))}
      </ul>
    </main>
  );
}
