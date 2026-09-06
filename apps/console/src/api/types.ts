// Typed data models for the Matrix Console.
//
// These mirror the wire contracts defined in the matrix-proto packages
// `matrix.market.v1` (proto/matrix/market/v1/market.proto) and
// `matrix.inference.v1` (proto/matrix/inference/v1/inference.proto). The console
// talks to a local `matrixd` node's MarketService (default :9091) and
// InferenceService (default :9092). We keep the models here as plain TypeScript
// so the UI and the transport layer share a single source of truth.

/** Job lifecycle, mirrors matrix.market.v1.JobStatus. */
export enum JobStatus {
  UNSPECIFIED = "JOB_STATUS_UNSPECIFIED",
  PENDING = "JOB_STATUS_PENDING",
  RUNNING = "JOB_STATUS_RUNNING",
  COMPLETED = "JOB_STATUS_COMPLETED",
  FAILED = "JOB_STATUS_FAILED",
  CANCELLED = "JOB_STATUS_CANCELLED",
}

/** Provider origin, mirrors matrix.market.v1.ProviderOrigin. */
export enum ProviderOrigin {
  UNSPECIFIED = "PROVIDER_ORIGIN_UNSPECIFIED",
  LOCAL = "PROVIDER_ORIGIN_LOCAL",
  REMOTE = "PROVIDER_ORIGIN_REMOTE",
}

/** Inference job lifecycle, mirrors matrix.inference.v1.InferenceJobStatus. */
export enum InferenceJobStatus {
  UNSPECIFIED = "INFERENCE_JOB_STATUS_UNSPECIFIED",
  PENDING = "INFERENCE_JOB_STATUS_PENDING",
  RUNNING = "INFERENCE_JOB_STATUS_RUNNING",
  COMPLETED = "INFERENCE_JOB_STATUS_COMPLETED",
  FAILED = "INFERENCE_JOB_STATUS_FAILED",
}

/** Chat role, mirrors matrix.inference.v1.ChatRole. */
export enum ChatRole {
  UNSPECIFIED = "CHAT_ROLE_UNSPECIFIED",
  SYSTEM = "CHAT_ROLE_SYSTEM",
  USER = "CHAT_ROLE_USER",
  ASSISTANT = "CHAT_ROLE_ASSISTANT",
}

export interface Provider {
  id: string;
  capacity: number;
  pricePerUnit: number;
  available: number;
  origin: ProviderOrigin;
  peerId: string;
}

export interface Job {
  id: string;
  buyer: string;
  provider: string;
  units: number;
  price: number;
  status: JobStatus;
  createdAt?: string;
  updatedAt?: string;
}

export interface Transaction {
  height: number;
  from: string;
  to: string;
  amount: number;
  nonce: number;
  hash: string;
  prevHash: string;
  timestamp?: string;
}

export interface Balance {
  account: string;
  balance: number;
}

export interface ChatMessage {
  role: ChatRole;
  content: string;
}

export interface InferenceJob {
  id: string;
  buyer: string;
  provider: string;
  model: string;
  status: InferenceJobStatus;
  completion: string;
  units: number;
  createdAt?: string;
  updatedAt?: string;
}

/**
 * ConsensusStatus is a console-side view derived from the consensus-backed
 * settlement chain exposed through MarketService.ListTransactions
 * (`chain_length` == committed block height) plus the validator set the node is
 * configured with. The matrixd consensus engine (services/core/internal/consensus)
 * commits settlement blocks with a leader-based fast BFT round; this view
 * surfaces the height, the current leader for the round, and the validator IDs.
 */
export interface ConsensusStatus {
  committedHeight: number;
  round: number;
  leader: string;
  quorum: number;
  validators: string[];
  headHash: string;
}

/** Connection state for the node connection panel. */
export type ConnectionState = "disconnected" | "connecting" | "connected" | "error";
