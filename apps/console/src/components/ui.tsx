// Small shared UI helpers for the console panels.

import React from "react";
import { InferenceJobStatus, JobStatus, ProviderOrigin } from "../api/types";

export function StatusBadge({ status }: { status: JobStatus | InferenceJobStatus }) {
  const label = status.replace(/^(JOB_STATUS_|INFERENCE_JOB_STATUS_)/, "");
  const cls = label.toLowerCase();
  return <span className={`badge ${cls}`}>{label}</span>;
}

export function OriginBadge({ origin }: { origin: ProviderOrigin }) {
  const label = origin.replace(/^PROVIDER_ORIGIN_/, "");
  return <span className={`badge ${label.toLowerCase()}`}>{label}</span>;
}

export function ErrorBox({ error }: { error: string | null }) {
  if (!error) return null;
  return <div className="error-box">⚠ {error}</div>;
}

export function OkBox({ message }: { message: string | null }) {
  if (!message) return null;
  return <div className="ok-box">✓ {message}</div>;
}

export function Empty({ children }: { children: React.ReactNode }) {
  return <div className="empty">{children}</div>;
}

export function truncate(s: string, n = 16): string {
  if (!s) return "—";
  return s.length > n ? `${s.slice(0, n)}…` : s;
}
