import {useState} from "react";
import {useQuery} from "@tanstack/react-query";
import {api, type Playback} from "./api";

export function EvidenceVault({playback}: {playback: Playback}) {
  const capsule = playback.capsule;
  return <section className="evidence-vault" aria-labelledby="evidence-vault-title">
    <div className="evidence-vault-heading">
      <div><p className="overline">Supporting evidence</p><h2 id="evidence-vault-title">Recorded context</h2><p>Open only the evidence needed for this investigation. Recorded values stay collapsed by default.</p></div>
    </div>
    <div className="evidence-groups">
      <details>
        <summary><span>Model configuration</span><small>{capsule.model_configuration?.requested_model ?? "Not reported"}</small></summary>
        {capsule.model_configuration ? <dl>
          <EvidenceRow label="Provider" value={capsule.model_configuration.provider} />
          <EvidenceRow label="Requested model" value={capsule.model_configuration.requested_model} />
          <EvidenceRow label="Provider-reported model" value={capsule.model_configuration.provider_reported_model} />
          {Object.entries(capsule.model_configuration.parameters ?? {}).map(([key, value]) => <EvidenceRow key={key} label={humanize(key)} value={prettyInline(value)} />)}
        </dl> : <p>No model configuration was recorded.</p>}
      </details>
      <details>
        <summary><span>Artifacts</span><small>{capsule.artifacts?.length ?? 0} recorded</small></summary>
        {capsule.artifacts?.length ? <ul className="evidence-list">{capsule.artifacts.map(artifact => <li key={artifact.id}><div><span className="semantic-label evidence">Evidence</span><strong>{artifact.logical_name}</strong><small>{artifact.type}{artifact.version ? ` · ${artifact.version}` : ""}</small></div>{artifact.payload && <EvidencePayload playback={playback} digest={artifact.payload.digest} mediaType={artifact.payload.media_type} />}</li>)}</ul> : <p>No artifacts were attached to this run.</p>}
      </details>
      <details>
        <summary><span>Evaluations</span><small>{capsule.evaluations?.length ?? 0} recorded</small></summary>
        {capsule.evaluations?.length ? <ul className="evidence-list">{capsule.evaluations.map(evaluation => <li key={`${evaluation.id}-${evaluation.version}`}><div><span className="semantic-label evidence">Evidence</span><strong>{evaluation.id}</strong><small>Version {evaluation.version}</small></div><div className="evaluation-result"><strong>{evaluation.score ?? "Recorded"}</strong>{evaluation.threshold !== undefined && <small>Threshold {evaluation.threshold}</small>}</div></li>)}</ul> : <p>No evaluator result was attached to this run.</p>}
      </details>
      <details>
        <summary><span>Provenance and technical identity</span><small>{capsule.provenance.commit_sha ? `Commit ${capsule.provenance.commit_sha.slice(0, 8)}` : "Partial provenance"}</small></summary>
        <dl>
          <EvidenceRow label="Repository" value={capsule.provenance.repository_url} />
          <EvidenceRow label="Commit" value={capsule.provenance.commit_sha} code />
          <EvidenceRow label="Runtime" value={capsule.provenance.runtime} />
          <EvidenceRow label="Container" value={capsule.provenance.container_id} code />
          <EvidenceRow label="Manifest digest" value={capsule.integrity.manifest_digest} code />
          <EvidenceRow label="Root digest" value={capsule.integrity.root_digest} code />
        </dl>
      </details>
    </div>
  </section>;
}

function EvidencePayload({playback, digest, mediaType}: {playback: Playback; digest?: string; mediaType?: string}) {
  const [open, setOpen] = useState(false);
  const availability = playback.payloads.find(item => item.digest === digest)?.availability;
  const content = useQuery({queryKey: ["payload", playback.capsule.capsule_id, digest], queryFn: () => api.payload(playback.capsule.capsule_id, digest!), enabled: open && Boolean(digest) && availability === "available", retry: false});
  if (!digest || availability !== "available") return <span className="evidence-unavailable">{availability?.replaceAll("_", " ") ?? "Not captured"}</span>;
  return <div className="evidence-payload"><button className="text-button" onClick={() => setOpen(value => !value)}>{open ? "Hide recorded value" : "View recorded value"}</button>{content.isLoading && <span role="status">Loading…</span>}{content.error && <span role="alert">Unavailable</span>}{open && content.data && <pre>{pretty(content.data, mediaType)}</pre>}</div>;
}

function EvidenceRow({label, value, code = false}: {label: string; value: unknown; code?: boolean}) {
  if (value === undefined || value === null || value === "") return null;
  return <div><dt>{label}</dt><dd>{code ? <code>{String(value)}</code> : String(value)}</dd></div>;
}

function humanize(value: string) { return value.replaceAll("_", " ").replace(/^./, match => match.toUpperCase()); }
function pretty(value: string, mediaType?: string) { if (!mediaType?.includes("json")) return value; try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; } }
function prettyInline(value: unknown) { return typeof value === "string" ? value : JSON.stringify(value); }
