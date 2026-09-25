import { useState, type FormEvent } from "react";
import { api, ApiError, auth } from "../api";

export default function Login({ onSignedIn }: { onSignedIn: () => void }) {
  const [key, setKey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    auth.set(key.trim());
    try {
      await api.endpoints(); // cheapest call that proves the key works
      onSignedIn();
    } catch (err) {
      auth.clear();
      if (err instanceof ApiError && err.status === 401) {
        setError("That key wasn't accepted. Paste the whole key, starting with hr_.");
      } else {
        setError("Couldn't reach the API. Check that it's running on port 8080.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login">
      <form className="login-form" onSubmit={submit}>
        <p className="brand">hookrelay</p>
        <h1>Sign in to your deliveries</h1>
        <label htmlFor="api-key">API key</label>
        <input
          id="api-key"
          type="password"
          autoComplete="off"
          spellCheck={false}
          placeholder="hr_…"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          aria-describedby={error ? "api-key-error" : "api-key-hint"}
          required
        />
        {error ? (
          <p id="api-key-error" className="field-error" role="alert">
            {error}
          </p>
        ) : (
          <p id="api-key-hint" className="hint">
            Use the key returned when the tenant was created. It stays in this tab and is forgotten when you close it.
          </p>
        )}
        <button type="submit" className="button-primary" disabled={busy || !key.trim()}>
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </main>
  );
}
