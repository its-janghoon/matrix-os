import { HardhatUserConfig } from "hardhat/config";
import "@nomicfoundation/hardhat-toolbox";
// hardhat-verify is bundled by hardhat-toolbox; importing it explicitly makes
// the `npx hardhat verify` task and the `etherscan` config block available.
import "@nomicfoundation/hardhat-verify";
// Load .env, because the docs have been telling operators to create one since
// before anything read it. README and .env.example both said "copy .env.example
// to .env and fill in your values"; nothing loaded it, so an operator who did
// exactly that got "refusing to deploy: PRIVATE_KEY is not set" and no clue why.
//
// It also removes the only alternative the repo documented, which was worse:
// an inline `PRIVATE_KEY=0x... npx hardhat run ...` puts a private key in shell
// history verbatim. A gitignored file read at config time does not.
import "dotenv/config";

// Optional deployer key for real networks. When unset, Hardhat's built-in
// local accounts are used for local dev. NEVER hardcode a real private key
// here - it must come from the PRIVATE_KEY env var only.
const PRIVATE_KEY = process.env.PRIVATE_KEY;
const accounts = PRIVATE_KEY ? [PRIVATE_KEY] : undefined;

// Real-network RPC endpoints. Every value comes from env and defaults to
// undefined when unset - NEVER hardcode a credential-bearing URL here.
const MAINNET_RPC_URL = process.env.MAINNET_RPC_URL;
const SEPOLIA_RPC_URL = process.env.SEPOLIA_RPC_URL;
const BASE_RPC_URL = process.env.BASE_RPC_URL;
const BASE_SEPOLIA_RPC_URL = process.env.BASE_SEPOLIA_RPC_URL;

// A mainnet-fork run of the in-process `hardhat` network is enabled only when
// MAINNET_FORK_RPC_URL is set (optionally gate with FORK=1). This lets the
// dry-run/fork simulation exercise the deploy path against forked mainnet state
// without ever broadcasting a real transaction.
const MAINNET_FORK_RPC_URL = process.env.MAINNET_FORK_RPC_URL;
const FORK_ENABLED =
  MAINNET_FORK_RPC_URL !== undefined && MAINNET_FORK_RPC_URL.trim() !== "";

// Etherscan API key for source verification, from env only.
//
// ONE key for every chain, and it must be a single STRING rather than a
// per-network map. That is not a style preference - hardhat-verify branches on
// the type: a string means "this is an etherscan.io key" and routes to the
// Etherscan V2 multichain API, while an object means "these are per-explorer
// keys" and keeps using each explorer's V1 API. The V1 endpoints this config
// used to name are now DEAD, and they fail in the least helpful way possible:
//
//   {"status":"0","message":"NOTOK","result":"You are using a deprecated V1
//    endpoint, switch to Etherscan API V2 ..."}
//
// which surfaces during `npm run verify:base-sepolia` - after the deploy has
// already been broadcast and paid for. Verified source is a launch gate in
// docs/runbooks/base-launch.md, so this failing late blocks the ceremony at its
// most expensive point.
//
// BASESCAN_API_KEY is still read, as a fallback only, so an operator who
// followed the older .env.example is not stranded; an etherscan.io key is what
// V2 actually wants.
const ETHERSCAN_API_KEY =
  process.env.ETHERSCAN_API_KEY ?? process.env.BASESCAN_API_KEY;

const config: HardhatUserConfig = {
  solidity: {
    version: "0.8.24",
    settings: {
      optimizer: {
        enabled: true,
        runs: 200,
      },
      // OpenZeppelin Contracts v5.x uses the `mcopy` opcode, which requires
      // the Cancun EVM. solc 0.8.24 supports Cancun but defaults to Shanghai.
      evmVersion: "cancun",
    },
  },
  networks: {
    // The in-process Hardhat network. When MAINNET_FORK_RPC_URL is set it forks
    // mainnet state so the deploy harness can be simulated against real chain
    // state; otherwise it is a clean local chain with built-in funded accounts.
    hardhat: FORK_ENABLED
      ? {
          forking: {
            url: MAINNET_FORK_RPC_URL as string,
          },
        }
      : {},
    // `npx hardhat node` and in-process runs use built-in funded accounts, so
    // no key configuration is required for local dev.
    localhost: {
      url: "http://127.0.0.1:8545",
      accounts,
    },
    // Ethereum mainnet - legacy-compatible target for the wrapped bridge.
    // Base is the primary production launch target. Both the RPC URL and the
    // deployer key are strictly env-provided here.
    //
    // chainId IS DECLARED, and that is a safety control rather than metadata.
    // Hardhat only verifies that the endpoint's chain matches when the network
    // entry names one; with it absent, `--network sepolia` meant nothing more
    // than "use whatever SEPOLIA_RPC_URL points at". A mainnet URL pasted into
    // the wrong variable passed every guard the deploy scripts have and
    // broadcast to mainnet, and the only thing standing in its way was a
    // "double-check the network" line on a human checklist. Now the mismatch is
    // an error before a transaction is signed.
    mainnet: {
      url: MAINNET_RPC_URL ?? "",
      accounts,
      chainId: 1,
    },
    // Sepolia testnet - dress-rehearsal target before mainnet.
    sepolia: {
      url: SEPOLIA_RPC_URL ?? "",
      accounts,
      chainId: 11155111,
    },
    // Base mainnet is the primary low-cost production target for wMATRIX
    // acquisition and redemption. Users pay their own Base ETH gas; this
    // configuration does not provide a relayer.
    base: {
      url: BASE_RPC_URL ?? "",
      accounts,
      chainId: 8453,
    },
    // Base Sepolia is the full bridge rehearsal network.
    baseSepolia: {
      url: BASE_SEPOLIA_RPC_URL ?? "",
      accounts,
      chainId: 84532,
    },
  },
  etherscan: {
    // A single string, deliberately. See the ETHERSCAN_API_KEY comment above:
    // the per-network object form pins hardhat-verify to the V1 APIs, which
    // Etherscan has retired for Base, Base Sepolia and everything else.
    apiKey: ETHERSCAN_API_KEY ?? "",
    // No customChains block. hardhat-verify 2.x already ships base (8453) and
    // baseSepolia (84532), and the entries this file used to override with
    // pointed at api.basescan.org and api-sepolia.basescan.org - the exact V1
    // hosts that now refuse every request. Overriding them re-broke what the
    // plugin gets right on its own.
  },
};

export default config;
