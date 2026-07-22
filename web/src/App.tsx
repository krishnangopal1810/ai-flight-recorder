import {useState} from "react";
import {useQuery, useQueryClient} from "@tanstack/react-query";
import {APIError, api, connection} from "./api";
import {BrandMark, ConnectionScreen, supportedService} from "./ConnectionScreen";
import {JobsExperience} from "./JobsWorkspace";
import {RecordWorkspace} from "./RecordWorkspace";
import {RunsExperience} from "./RunsWorkspace";
import {useRoute} from "./routing";

export function App() {
  const {route, navigate} = useRoute();
  const client = useQueryClient();
  const session = useQuery({queryKey: ["session", connection.serviceURL], queryFn: api.session, retry: false});
  const [density, setDensity] = useState(() => localStorage.getItem("flight_density") ?? "comfortable");

  if (session.isLoading) return <div className="boot-screen" role="status"><BrandMark /><span>Connecting to recorded evidence…</span></div>;
  const incompatible = session.data && !supportedService(session.data.service_version);
  if (session.error || incompatible || !session.data?.connected || (!session.data.scopes.includes("read") && !session.data.scopes.includes("admin"))) {
    const error = incompatible ? new APIError(426, "unsupported_version", "This service version is not supported.", "Connect to a Flight Recorder service with API major version 1.") : session.error;
    return <ConnectionScreen error={error} />;
  }

  function disconnect() {
    connection.disconnect();
    client.clear();
    navigate("/runs", true);
  }
  function toggleDensity() {
    const next = density === "comfortable" ? "compact" : "comfortable";
    localStorage.setItem("flight_density", next);
    setDensity(next);
  }

  return <div className={`app-shell density-${density}`}>
    <a className="skip-link" href="#main-content">Skip to investigation</a>
    <header className="app-header">
      <button className="brand-button" onClick={() => navigate("/runs")} aria-label="AI Flight Recorder runs"><BrandMark /><span><strong>AI Flight Recorder</strong><small>Investigation workspace</small></span></button>
      <nav aria-label="Primary navigation"><button className={route.kind === "runs" || route.kind === "run" ? "active" : ""} onClick={() => navigate("/runs")}>Runs</button><button className={route.kind === "record" ? "active" : ""} onClick={() => navigate("/record")}>Add run</button><button className={route.kind === "jobs" || route.kind === "job" ? "active" : ""} onClick={() => navigate("/jobs")}>Replays</button></nav>
      <div className="header-actions"><span className="connection-state"><i />Connected · {session.data.scopes.join(", ")}</span><button className="icon-button" onClick={toggleDensity} title={`Use ${density === "comfortable" ? "compact" : "comfortable"} density`} aria-label="Toggle display density">{density === "comfortable" ? "Compact" : "Comfortable"}</button><button className="icon-button" onClick={disconnect}>Disconnect</button></div>
    </header>
    <main id="main-content" tabIndex={-1}>
      {route.kind === "record" && <RecordWorkspace scopes={session.data.scopes} releaseVersion={session.data.release_version} navigate={navigate} />}
      {(route.kind === "runs" || route.kind === "run") && <RunsExperience route={route} navigate={navigate} />}
      {(route.kind === "jobs" || route.kind === "job") && <JobsExperience route={route} navigate={navigate} />}
    </main>
  </div>;
}
