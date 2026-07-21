import {useEffect, useState} from "react";

export type Stage = "understand" | "reproduce" | "compare";
export type Route =
  | {kind: "record"}
  | {kind: "runs"}
  | {kind: "run"; runID: string; stage: Stage; candidateID?: string}
  | {kind: "jobs"}
  | {kind: "job"; jobID: string};

export function parseRoute(pathname = window.location.pathname): Route {
  const segments = pathname.split("/").filter(Boolean).map(decodeURIComponent);
  if (segments[0] === "record") return {kind: "record"};
  if (segments[0] === "capsules" && segments[1]) return {kind: "run", runID: segments[1], stage: "understand"};
  if (segments[0] === "runs" && segments[1]) {
    const stage = segments[2] === "reproduce" || segments[2] === "compare" ? segments[2] : "understand";
    return {kind: "run", runID: segments[1], stage, candidateID: stage === "compare" ? segments[3] : undefined};
  }
  if (segments[0] === "jobs" && segments[1]) return {kind: "job", jobID: segments[1]};
  if (segments[0] === "jobs") return {kind: "jobs"};
  return {kind: "runs"};
}

export function useRoute() {
  const [route, setRoute] = useState<Route>(() => parseRoute());
  useEffect(() => {
    const update = () => setRoute(parseRoute());
    window.addEventListener("popstate", update);
    return () => window.removeEventListener("popstate", update);
  }, []);
  const navigate = (path: string, replace = false) => {
    if (replace) history.replaceState({}, "", path);
    else history.pushState({}, "", path);
    setRoute(parseRoute(path.split("?")[0]));
    window.scrollTo({top: 0, behavior: "instant"});
  };
  return {route, navigate};
}

export function runPath(runID: string, stage: Stage, candidateID?: string): string {
  const base = `/runs/${encodeURIComponent(runID)}/${stage}`;
  return candidateID ? `${base}/${encodeURIComponent(candidateID)}` : base;
}
