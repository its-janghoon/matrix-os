// In-memory demo backend for the Matrix Console.
//
// This is NOT a fake UI: it implements the exact same MatrixClient interface as
// the live ConnectMatrixClient and models the real settlement semantics of
// matrixd's marketplace (services/core/internal/market + internal/token +
// internal/consensus):
//   - RegisterProvider seeds available == capacity.
//   - SubmitJob reserves capacity (available -= units) and creates a PENDING job;
//     no credits move.
//   - CompleteJob transfers price buyer -> provider on the ledger, appends a
//     hash-chained transaction (the committed-height view), and marks COMPLETED.
//   - CancelJob returns reserved capacity; no credits move.
//   - Inference jobs reserve one unit and settle buyer -> provider on fulfill,
//     with the completion produced by a deterministic local echo runner (the
//     GPU-free stub backend described in the inference feature).
//
// Because the console renders identically against this backend and a real node,
// screenshots taken offline reflect the true data flow. Point the connection at
// a running matrixd (Connect endpoint) to switch to live data.

import type {
  Balance,
  InferenceJob,
  Job,
  Provider,
  Transaction,
} from "./types";
import {
  InferenceJobStatus,
  JobStatus,
  ProviderOrigin,
} from "./types";
import type { MatrixClient, SubmitInferenceParams } from "./client";

function sha256ish(seed: string): string {
  // Small non-cryptographic hex digest for display only (the real chain uses
  // SHA-256; the console only shows the hex, never verifies it).
  let h = 0x811c9dc5;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  const hex = (h >>> 0).toString(16).padStart(8, "0");
  return (hex + hex + hex + hex).slice(0, 64);
}

const GENESIS = "0".repeat(64);

export class DemoMatrixClient implements MatrixClient {
  private providers = new Map<string, Provider>();
  private jobs = new Map<string, Job>();
  private inferenceJobs = new Map<string, InferenceJob>();
  private balances = new Map<string, number>();
  private chain: Transaction[] = [];
  private jobSeq = 0;

  constructor() {
    this.seed();
  }

  private seed(): void {
    // A local order-book provider and a remote gossip-discovered provider.
    this.providers.set("gpu-local-01", {
      id: "gpu-local-01",
      capacity: 100,
      pricePerUnit: 5,
      available: 100,
      origin: ProviderOrigin.LOCAL,
      peerId: "",
    });
    this.providers.set("gpu-remote-eu", {
      id: "gpu-remote-eu",
      capacity: 250,
      pricePerUnit: 4,
      available: 250,
      origin: ProviderOrigin.REMOTE,
      peerId: "12D3KooWEUdemoPeerRemoteProviderId000000000000",
    });
    this.providers.set("llm-remote-us", {
      id: "llm-remote-us",
      capacity: 500,
      pricePerUnit: 3,
      available: 500,
      origin: ProviderOrigin.REMOTE,
      peerId: "12D3KooWUSdemoPeerInferenceProvider00000000000",
    });

    // Fund a demo buyer so jobs can settle.
    this.balances.set("buyer-alice", 10_000);
    this.balances.set("buyer-bob", 2_500);
  }

  private appendTx(from: string, to: string, amount: number): Transaction {
    const height = this.chain.length;
    const prevHash = height === 0 ? GENESIS : this.chain[height - 1]!.hash;
    const tx: Transaction = {
      height,
      from,
      to,
      amount,
      nonce: this.chain.filter((t) => t.from === from).length,
      hash: sha256ish(`${from}:${to}:${amount}:${height}:${prevHash}`),
      prevHash,
      timestamp: new Date().toISOString(),
    };
    this.chain.push(tx);
    return tx;
  }

  private settle(from: string, to: string, amount: number): void {
    const fromBal = this.balances.get(from) ?? 0;
    if (fromBal < amount) {
      throw new Error(`insufficient balance: ${from} has ${fromBal}, needs ${amount}`);
    }
    this.balances.set(from, fromBal - amount);
    this.balances.set(to, (this.balances.get(to) ?? 0) + amount);
    this.appendTx(from, to, amount);
  }

  async ping(): Promise<void> {
    return;
  }

  async listProviders(includeRemote: boolean): Promise<Provider[]> {
    const all = Array.from(this.providers.values());
    const filtered = includeRemote ? all : all.filter((p) => p.origin === ProviderOrigin.LOCAL);
    return filtered.sort((a, b) => a.id.localeCompare(b.id));
  }

  async registerProvider(id: string, capacity: number, pricePerUnit: number): Promise<Provider> {
    if (!id) throw new Error("provider id is required");
    if (capacity <= 0) throw new Error("capacity must be > 0");
    if (pricePerUnit <= 0) throw new Error("price per unit must be > 0");
    const provider: Provider = {
      id,
      capacity,
      pricePerUnit,
      available: capacity,
      origin: ProviderOrigin.LOCAL,
      peerId: "",
    };
    this.providers.set(id, provider);
    return provider;
  }

  async submitJob(buyer: string, providerId: string, units: number): Promise<Job> {
    const provider = this.providers.get(providerId);
    if (!provider) throw new Error(`unknown provider: ${providerId}`);
    if (units <= 0) throw new Error("units must be > 0");
    if (provider.available < units) throw new Error("insufficient provider capacity");
    const price = units * provider.pricePerUnit;
    if ((this.balances.get(buyer) ?? 0) < price) {
      throw new Error(`buyer ${buyer} cannot afford ${price} credits`);
    }
    provider.available -= units;
    const id = `job-${++this.jobSeq}`;
    const now = new Date().toISOString();
    const job: Job = {
      id,
      buyer,
      provider: providerId,
      units,
      price,
      status: JobStatus.PENDING,
      createdAt: now,
      updatedAt: now,
    };
    this.jobs.set(id, job);
    return job;
  }

  async listJobs(buyer?: string): Promise<Job[]> {
    let jobs = Array.from(this.jobs.values());
    if (buyer) jobs = jobs.filter((j) => j.buyer === buyer);
    return jobs.sort((a, b) => (a.createdAt ?? "").localeCompare(b.createdAt ?? ""));
  }

  async completeJob(id: string): Promise<Job> {
    const job = this.jobs.get(id);
    if (!job) throw new Error(`unknown job: ${id}`);
    if (job.status !== JobStatus.PENDING && job.status !== JobStatus.RUNNING) {
      throw new Error(`job ${id} is not completable (status ${job.status})`);
    }
    this.settle(job.buyer, job.provider, job.price);
    job.status = JobStatus.COMPLETED;
    job.updatedAt = new Date().toISOString();
    return job;
  }

  async cancelJob(id: string): Promise<Job> {
    const job = this.jobs.get(id);
    if (!job) throw new Error(`unknown job: ${id}`);
    if (job.status !== JobStatus.PENDING && job.status !== JobStatus.RUNNING) {
      throw new Error(`job ${id} is not cancellable (status ${job.status})`);
    }
    const provider = this.providers.get(job.provider);
    if (provider) provider.available += job.units;
    job.status = JobStatus.CANCELLED;
    job.updatedAt = new Date().toISOString();
    return job;
  }

  async getBalance(account: string): Promise<Balance> {
    return { account, balance: this.balances.get(account) ?? 0 };
  }

  async listTransactions(
    startHeight: number,
    limit: number,
  ): Promise<{ transactions: Transaction[]; chainLength: number }> {
    let txs = this.chain.filter((t) => t.height >= startHeight);
    if (limit > 0) txs = txs.slice(0, limit);
    return { transactions: txs, chainLength: this.chain.length };
  }

  async submitInference(params: SubmitInferenceParams): Promise<InferenceJob> {
    const provider = this.providers.get(params.provider);
    if (!provider) throw new Error(`unknown provider: ${params.provider}`);
    const units = params.unitsEstimate && params.unitsEstimate > 0 ? params.unitsEstimate : 1;
    if (provider.available < units) throw new Error("insufficient provider capacity");
    const price = units * provider.pricePerUnit;
    if ((this.balances.get(params.buyer) ?? 0) < price) {
      throw new Error(`buyer ${params.buyer} cannot afford ${price} credits`);
    }
    provider.available -= units;
    const id = `inf-${++this.jobSeq}`;
    const now = new Date().toISOString();
    const job: InferenceJob = {
      id,
      buyer: params.buyer,
      provider: params.provider,
      model: params.model,
      status: InferenceJobStatus.PENDING,
      completion: "",
      units,
      createdAt: now,
      updatedAt: now,
    };
    // Stash the prompt on the job id -> prompt map via closure store.
    this.pendingPrompts.set(id, params.prompt);
    this.inferenceJobs.set(id, job);
    return job;
  }

  private pendingPrompts = new Map<string, string>();

  async fulfillInference(id: string): Promise<InferenceJob> {
    const job = this.inferenceJobs.get(id);
    if (!job) throw new Error(`unknown inference job: ${id}`);
    if (job.status !== InferenceJobStatus.PENDING && job.status !== InferenceJobStatus.RUNNING) {
      throw new Error(`inference job ${id} is not fulfillable (status ${job.status})`);
    }
    const provider = this.providers.get(job.provider)!;
    const price = job.units * provider.pricePerUnit;
    this.settle(job.buyer, job.provider, price);
    provider.available += job.units;
    const prompt = this.pendingPrompts.get(id) ?? "";
    // Deterministic local echo runner — the GPU-free stub inference backend.
    job.completion =
      `[${job.model} via ${job.provider}] ` +
      (prompt
        ? `Echo completion for: "${prompt.slice(0, 240)}"`
        : "Echo completion (empty prompt).");
    job.status = InferenceJobStatus.COMPLETED;
    job.updatedAt = new Date().toISOString();
    return job;
  }

  async getInferenceJob(id: string): Promise<InferenceJob> {
    const job = this.inferenceJobs.get(id);
    if (!job) throw new Error(`unknown inference job: ${id}`);
    return job;
  }
}
