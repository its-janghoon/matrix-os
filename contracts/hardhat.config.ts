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

// A mainnet-fork run of the in-process `hardhat` network is enabled only when
// MAINNET_FORK_RPC_URL is set (optionally gate with FORK=1). This lets the
// dry-run/fork simulation exercise the deploy path against forked mainnet state
// without ever broadcasting a real transaction.
const MAINNET_FORK_RPC_URL = process.env.MAINNET_FORK_RPC_URL;
const FORK_ENABLED =
  MAINNET_FORK_RPC_URL !== undefined && MAINNET_FORK_RPC_URL.trim() !== "";

// Etherscan API key for source verification, from env only.
const ETHERSCAN_API_KEY = process.env.ETHERSCAN_API_KEY;

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
    // Ethereum mainnet - production target for the WRAPPED ERC-20 + bridge.
    // Both the RPC URL and the deployer key are strictly env-provided.
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
  },
  etherscan: {
    // Reads the Etherscan API key from env only; empty string keeps the config
    // valid when the key is unset (verification simply won't be usable).
    apiKey: ETHERSCAN_API_KEY ?? "",
  },
};

export default config;
