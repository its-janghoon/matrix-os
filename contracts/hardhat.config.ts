import { HardhatUserConfig } from "hardhat/config";
import "@nomicfoundation/hardhat-toolbox";

// Optional deployer key for real networks. When unset, Hardhat's built-in
// local accounts are used. NEVER hardcode a real private key here.
const PRIVATE_KEY = process.env.PRIVATE_KEY;

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
    // The in-process Hardhat network and `npx hardhat node` use built-in
    // funded accounts, so no key configuration is required for local dev.
    localhost: {
      url: "http://127.0.0.1:8545",
      accounts: PRIVATE_KEY ? [PRIVATE_KEY] : undefined,
    },
  },
};

export default config;
