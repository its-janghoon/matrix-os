import { ethers } from "hardhat";
import { readFileSync } from "node:fs";

/** Reviewed, non-secret constructor inputs frozen before the founder ceremony. */
export interface FounderCeremonyInput {
  network: string;
  chainId: bigint;
  token: string;
  beneficiary: string;
  start: number;
  cliffDuration: number;
  duration: number;
  allocation: bigint;
}

function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("founder ceremony input must be a JSON object");
  }
  return value as Record<string, unknown>;
}

function stringField(value: Record<string, unknown>, name: string): string {
  const field = value[name];
  if (typeof field !== "string" || field.trim() === "") {
    throw new Error(`founder ceremony field '${name}' must be a non-empty string`);
  }
  return field.trim();
}

function integerField(value: Record<string, unknown>, name: string): number {
  const field = value[name];
  if (typeof field !== "number" || !Number.isSafeInteger(field) || field <= 0) {
    throw new Error(`founder ceremony field '${name}' must be a positive safe integer`);
  }
  return field;
}

function bigintField(value: Record<string, unknown>, name: string): bigint {
  const field = stringField(value, name);
  if (!/^\d+$/.test(field) || BigInt(field) <= 0n) {
    throw new Error(`founder ceremony field '${name}' must be a positive base-10 integer string`);
  }
  return BigInt(field);
}

export function parseFounderCeremonyInput(raw: string): FounderCeremonyInput {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch (error) {
    throw new Error(`founder ceremony input must be JSON: ${(error as Error).message}`);
  }
  const value = object(parsed);
  return {
    network: stringField(value, "network"),
    chainId: bigintField(value, "chainId"),
    token: ethers.getAddress(stringField(value, "token")),
    beneficiary: ethers.getAddress(stringField(value, "beneficiary")),
    start: integerField(value, "start"),
    cliffDuration: integerField(value, "cliffDuration"),
    duration: integerField(value, "duration"),
    allocation: bigintField(value, "allocation"),
  };
}

export function loadFounderCeremonyInput(path: string): FounderCeremonyInput {
  return parseFounderCeremonyInput(readFileSync(path, "utf8"));
}

export function assertFounderCeremonyMatches(
  frozen: FounderCeremonyInput,
  actual: FounderCeremonyInput,
  source: string
): void {
  const fields: Array<keyof FounderCeremonyInput> = [
    "network",
    "chainId",
    "token",
    "beneficiary",
    "start",
    "cliffDuration",
    "duration",
    "allocation",
  ];
  for (const field of fields) {
    const expected = frozen[field];
    const observed = actual[field];
    const equal =
      typeof expected === "string" && typeof observed === "string"
        ? expected.toLowerCase() === observed.toLowerCase()
        : expected === observed;
    if (!equal) {
      throw new Error(
        `founder ceremony ${field} mismatch: frozen '${String(expected)}', ${source} '${String(observed)}'`
      );
    }
  }
}
