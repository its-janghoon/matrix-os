// Lists swaps against the pool since it was created.
//
// This is here because the pool's live tick had already drifted from its
// initialization tick minutes after creation, and there are only two
// explanations for that: someone traded, or the pool being read is not the pool
// that was created. Reading the Swap events settles which, and turns "the
// number looks slightly off" into a list of actual trades.

const { ethers } = require('ethers');

const RPC = process.env.RPC_URL || 'https://mainnet.base.org';
const POOL_MANAGER = '0x498581fF718922c3f8e6A244956aF099B2652b2b';
const POOL_ID = process.argv[2];
const FROM_BLOCK = Number(process.argv[3] || 51208000);

if (!POOL_ID) {
  console.error('usage: node pool-swaps.js <poolId> [fromBlock]');
  process.exit(1);
}

const iface = new ethers.Interface([
  'event Swap(bytes32 indexed id, address indexed sender, int128 amount0, int128 amount1, uint160 sqrtPriceX96, uint128 liquidity, int24 tick, uint24 fee)',
]);

(async () => {
  const p = new ethers.JsonRpcProvider(RPC);
  const latest = await p.getBlockNumber();

  const topic = iface.getEvent('Swap').topicHash;
  const logs = await p.getLogs({
    address: POOL_MANAGER,
    topics: [topic, POOL_ID],
    fromBlock: FROM_BLOCK,
    toBlock: latest,
  });

  console.log(`blocks ${FROM_BLOCK}..${latest}  swaps: ${logs.length}`);
  for (const log of logs) {
    const s = iface.parseLog(log);
    const [, sender, a0, a1, sqrtPriceX96, liquidity, tick, fee] = s.args;
    // The amounts are the SWAPPER's deltas, not the pool's: negative means the
    // swapper paid it in, positive means the swapper took it out. Reading these
    // the other way round reports every buy as a sell, and the tell is that the
    // tick then moves the wrong way for the trade being described.
    const dir = a0 < 0n ? 'BUY wMATRIX (paid ETH)' : 'SELL wMATRIX (received ETH)';
    console.log(`\nblock ${log.blockNumber}  tx ${log.transactionHash}`);
    console.log('  ', dir);
    console.log('   sender  ', sender);
    console.log('   ETH     ', ethers.formatEther(a0));
    console.log('   wMATRIX ', ethers.formatUnits(a1, 18));
    console.log('   tick    ', tick.toString());
  }
})().catch((e) => {
  console.error('FAILED:', e.message);
  process.exit(1);
});
