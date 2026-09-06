# Matrix Compute Token (MATRIX)

`contracts/` is the Solidity project for **MATRIX**, the ERC-20 settlement and
earning currency of the Matrix OS compute marketplace. Buyers pay for LLM
compute and API responses in MATRIX, and providers who contribute compute earn
MATRIX. The token is a standards-compliant ERC-20 built on OpenZeppelin's
audited contracts, so it can later be listed on an exchange.

## Token

| Property        | Value                                            |
| --------------- | ------------------------------------------------ |
| Name            | `Matrix Compute Token`                           |
| Symbol          | `MATRIX`                                          |
| Decimals        | `18`                                             |
| Initial supply  | `100,000,000 MATRIX` minted to the deployer      |
| Max supply      | `1,000,000,000 MATRIX` (hard cap, `MAX_SUPPLY`)  |
| Standards       | ERC-20, ERC-2612 (`permit`)                      |

`MatrixToken` inherits OpenZeppelin's `ERC20`, `ERC20Permit`, and `Ownable`:

- **ERC20** — full standard interface (`name`, `symbol`, `decimals`,
  `totalSupply`, `balanceOf`, `transfer`, `approve`, `transferFrom`,
  `allowance`) and the `Transfer` / `Approval` events.
- **ERC20Permit** — EIP-2612 gasless approvals via `permit()`.
- **Ownable** — gates minting so new supply can only be created by the owner.

### Mint policy

The constructor mints the initial supply to the deployer. The owner can later
`mint(to, amount)` to back marketplace earnings (paying providers), but total
supply can **never** exceed `MAX_SUPPLY`. Any mint that would cross the cap
reverts with `MaxSupplyExceeded`, and non-owners cannot mint at all.

## Role in the marketplace

MATRIX is the unit of account for the compute marketplace. A buyer's payment
for a job is denominated in MATRIX; when a provider fulfils that job, the
provider is credited in MATRIX. The owner-controlled, capped mint lets the
network issue provider rewards without allowing unbounded inflation.

## Commands

Install dependencies (Node 22):

```sh
cd contracts
npm install
```

Compile the contracts:

```sh
npx hardhat compile
```

Run the test suite (chai):

```sh
npx hardhat test
```

Run a local development node:

```sh
npx hardhat node
```

Deploy to the local node (in a second terminal, while `hardhat node` runs):

```sh
npx hardhat run scripts/deploy.ts --network localhost
```

The deploy script logs the deployed address and supply. It reads an optional
deployer key from the `PRIVATE_KEY` environment variable for real networks and
falls back to Hardhat's built-in funded accounts for local development. **No
real private keys or secrets are committed to this repository.**

## Toolchain notes

- Hardhat is pinned to the v2 line (`hardhat@^2.22`) with
  `@nomicfoundation/hardhat-toolbox@^5` and `@openzeppelin/contracts@^5`.
- Solidity `0.8.24` with the optimizer enabled (200 runs). The EVM target is
  `cancun` because OpenZeppelin Contracts v5.x uses the `mcopy` opcode.
- `solc` is downloaded automatically by Hardhat; no system `solc`/`forge` is
  required.
