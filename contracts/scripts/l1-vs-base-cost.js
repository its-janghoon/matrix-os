// Compares what THIS transaction actually cost on Base against what the same
// gas usage would cost on Ethereum L1 at the current L1 gas price, using two
// independent public RPCs (no shared provider, so a single flaky endpoint
// cannot bias both sides of the comparison).

const { ethers } = require('ethers');

const BASE_RPC = process.env.RPC_URL || 'https://mainnet.base.org';
const ETH_RPC = 'https://ethereum-rpc.publicnode.com';

const GAS_USED = 449426n; // from the pool-creation tx already decoded

(async () => {
  const base = new ethers.JsonRpcProvider(BASE_RPC);
  const eth = new ethers.JsonRpcProvider(ETH_RPC);

  const rcpt = await base.getTransactionReceipt(
    '0x6db5012e48a3fefec0b1fe694875ff16533bc73b9c307a06abaf308ce26a9490'
  );
  const baseEffGasPrice = rcpt.gasPrice ?? rcpt.effectiveGasPrice;
  const baseCostEth = baseEffGasPrice * GAS_USED;

  await new Promise((r) => setTimeout(r, 500));
  const ethFeeData = await eth.getFeeData();
  const l1GasPrice = ethFeeData.gasPrice;
  const l1CostEth = l1GasPrice * GAS_USED;

  const ethUsd = 2533; // from the pool's own initial price, already confirmed on-chain

  console.log('gas used (same tx)   ', GAS_USED.toString());
  console.log('\n-- Base (actual) --');
  console.log('gas price   ', ethers.formatUnits(baseEffGasPrice, 'gwei'), 'gwei');
  console.log('cost        ', ethers.formatEther(baseCostEth), 'ETH  ($' + (Number(ethers.formatEther(baseCostEth)) * ethUsd).toFixed(4) + ')');
  console.log('\n-- Ethereum L1 (same gas units, current L1 price) --');
  console.log('gas price   ', ethers.formatUnits(l1GasPrice, 'gwei'), 'gwei');
  console.log('cost        ', ethers.formatEther(l1CostEth), 'ETH  ($' + (Number(ethers.formatEther(l1CostEth)) * ethUsd).toFixed(2) + ')');
  console.log('\nratio       ', (Number(l1CostEth) / Number(baseCostEth)).toFixed(0) + 'x more expensive on L1');
})().catch((e) => {
  console.error('FAILED:', e.message);
  process.exit(1);
});
