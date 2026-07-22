import {FormEvent, useState} from "react";
import {useQueryClient} from "@tanstack/react-query";
import {APIError, api, connection} from "./api";

export function ConnectionScreen({error}: {error?: Error | null}) {
  const client = useQueryClient();
  const [serviceURL, setServiceURL] = useState(connection.serviceURL);
  const [token, setToken] = useState("");
  const [visible, setVisible] = useState(false);
  const [testing, setTesting] = useState(false);
  const [localError, setLocalError] = useState<Error | null>(null);

  async function connect(event?: FormEvent) {
    event?.preventDefault();
    setTesting(true);
    setLocalError(null);
    try {
      const normalized = new URL(serviceURL);
      if (normalized.username || normalized.password) throw new Error("The service URL must not contain credentials.");
      if (normalized.protocol !== "https:" && !["127.0.0.1", "localhost", "::1"].includes(normalized.hostname)) {
        throw new Error("Remote Flight Recorder connections require HTTPS.");
      }
      connection.configure(normalized.origin, token.trim());
      const session = await api.session();
      if (!supportedService(session.service_version)) throw new APIError(426, "unsupported_version", "This service version is not supported.", "Connect to a Flight Recorder service with API major version 1.");
      if (!session.connected) throw new APIError(401, "authentication", "A read token is required.", "Enter a token with read access.");
      if (!session.scopes.includes("read") && !session.scopes.includes("admin")) {
        throw new APIError(403, "authentication", "This token cannot inspect recorded runs.", "Use a token with read or admin scope.");
      }
      await client.invalidateQueries();
    } catch (caught) {
      setLocalError(caught instanceof Error ? caught : new Error("Connection failed."));
    } finally {
      setTesting(false);
    }
  }

  const visibleError = localError ?? error;
  return <main className="connection-page">
    <section className="connection-card" aria-labelledby="connection-title">
      <div className="brand-lockup"><BrandMark /><div><span>AI Flight Recorder</span><small>Investigation workspace</small></div></div>
      <div className="connection-intro"><p className="overline">Connect</p><h1 id="connection-title">Connect to your Flight Recorder</h1><p>Open a failed agent run, reproduce it safely, and find the first important change.</p></div>
      {visibleError && <ConnectionError error={visibleError} />}
      <form onSubmit={connect} className="connection-form">
        <label><span>Service URL</span><input aria-label="Service URL" type="url" value={serviceURL} onChange={event => setServiceURL(event.target.value)} required /></label>
        <label><span>Access token</span><div className="token-input"><input aria-label="Access token" type={visible ? "text" : "password"} value={token} onChange={event => setToken(event.target.value)} placeholder="Token with read access" autoComplete="off" /><button type="button" className="text-button" onClick={() => setVisible(value => !value)}>{visible ? "Hide" : "Show"}</button></div><small>Stored only for this browser session.</small></label>
        <button className="primary-button" type="submit" disabled={testing}>{testing ? "Testing connection…" : "Test and connect"}</button>
      </form>
      <div className="connection-help"><a href="https://github.com/krishnangopal1810/ai-flight-recorder#record-an-agent">Record an agent</a><span>·</span><code>flight record -- &lt;command&gt;</code></div>
    </section>
  </main>;
}

function ConnectionError({error}: {error: Error}) {
  const apiError = error instanceof APIError ? error : null;
  const title = apiError?.code === "offline" ? "Service unavailable" : apiError?.status === 403 ? "Read access required" : "Connection needs attention";
  return <div className="connection-error" role="alert"><strong>{title}</strong><p>{friendlyMessage(error)}</p>{apiError?.remediation && <small>{apiError.remediation}</small>}</div>;
}

function friendlyMessage(error: Error): string {
  if (!(error instanceof APIError)) return error.message;
  if (error.status === 401) return "The token is missing, invalid, or revoked.";
  if (error.status === 403) return "The token does not include permission to inspect recorded runs.";
  if (error.code === "offline") return "The service URL could not be reached.";
  if (error.code === "unsupported_version") return "This inspector and Flight Recorder service use incompatible API versions.";
  return "Flight Recorder could not complete the connection test.";
}

export function supportedService(version: string): boolean { return version.split(".")[0] === "1"; }

export function BrandMark() {
  return <span className="brand-mark" aria-hidden="true"><img src="/brand-mark.svg" alt="" /></span>;
}
