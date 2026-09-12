// Reads the live pool state from the v4 StateView contract, which is the only
// way to confirm the position is actually earning: the creation receipt proves
// the deposit was accepted, not that the pool still holds it.
//
// The PoolId is passed in rather than recomputed, because it comes from the
// Initialize event and is therefore already exact. Recomputing it from guessed
// PoolKey fields is how you end up reading a nonexistent pool and reporting
// zeros as a failure.

const { ethers } = require('ethers');

const RPC = process.env.RPC_URL || 'https://mainnet.base.org';
const STATE_VIEW = '0xA3c0c9b65baD0b08107Aa264b0f3dB444b867A71'; // v4 StateView, Base
const POOL_MANAGER = '0x498581fF718922c3f8e6A244956aF099B2652b2b';
const WMATRIX = '0x0ec1C40829B3A5C2349Afa8C4Da6dC8B727fC87c';

const POOL_ID = process.argv[2];
if (!POOL_ID) {
  console.error('usage: node pool-state.js <poolId>');
  process.exit(1);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const p = new ethers.JsonRpcProvider(RPC);
  const sv = new ethers.Contract(
    STATE_VIEW,
    [
      'function getSlot0(bytes32 poolId) view returns (uint160 sqrtPriceX96, int24 tick, uint24 protocolFee, uint24 lpFee)',
      'function getLiquidity(bytes32 poolId) view returns (uint128 liquidity)',
    ],
    p
  );

  const slot0 = await sv.getSlot0(POOL_ID);
  await sleep(700);
  const liq = await sv.getLiquidity(POOL_ID);

  console.log('=== live pool state ===');
  console.log('sqrtPriceX96 ', slot0[0].toString());
  console.log('tick         ', slot0[1].toString());
  console.log('protocolFee  ', slot0[2].toString());
  console.log('lpFee        ', slot0[3].toString(), '=', Number(slot0[3]) / 10000 + '%');
  console.log('liquidity    ', liq.toString());
  console.log('pool is live ', liq > 0n ? 'YES' : 'NO - liquidity is zero');

  // What the PoolManager actually custodies for this pair.
  await sleep(700);
  const ethHeld = await p.getBalance(POOL_MANAGER);
  await sleep(700);
  const t = new ethers.Contract(WMATRIX, ['function balanceOf(address) view returns (uint256)'], p);
  const wmHeld = await t.balanceOf(POOL_MANAGER);

  console.log('\n=== PoolManager custody (all pools, not just this one) ===');
  console.log('ETH     ', ethers.formatEther(ethHeld));
  console.log('wMATRIX ', ethers.formatUnits(wmHeld, 18));
})().catch((e) => {
  console.error('FAILED:', e.message);
  process.exit(1);
});
