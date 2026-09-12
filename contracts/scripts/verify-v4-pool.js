// Reads the Uniswap v4 pool-creation transaction and reports what it actually
// did on chain: the PoolKey the pool was initialized with, the price it was
// initialized at, and the liquidity range that was funded.
//
// This exists because the pool parameters cannot be recovered from the UI after
// the fact and cannot be guessed: v4 allows arbitrary fee values, and the
// PoolId is the hash of the whole PoolKey, so one wrong field yields a
// completely different id and every later lookup silently reads an empty pool.
// The Initialize event carries every field, so it is the only source worth
// trusting here.

const { ethers } = require('ethers');

const TX = process.argv[2];
if (!TX) {
  console.error('usage: node verify-v4-pool.js <txHash>');
  process.exit(1);
}

const RPC = process.env.RPC_URL || 'https://mainnet.base.org';

// Uniswap v4 on Base.
const POOL_MANAGER = '0x498581fF718922c3f8e6A244956aF099B2652b2b';
const POSITION_MANAGER = '0x7C5f5A4bBd8fD63184577525326123B519429bDc';

const WMATRIX = '0x1c2cE9CBAcBb0D2d1e1B0eD7bd8fB3aC2eAB6Dc7'; // placeholder, replaced below

const iface = new ethers.Interface([
  'event Initialize(bytes32 indexed id, address indexed currency0, address indexed currency1, uint24 fee, int24 tickSpacing, address hooks, uint160 sqrtPriceX96, int24 tick)',
  'event ModifyLiquidity(bytes32 indexed id, address indexed sender, int24 tickLower, int24 tickUpper, int256 liquidityDelta, bytes32 salt)',
  'event Transfer(address indexed from, address indexed to, uint256 indexed id)',
]);

const erc20 = new ethers.Interface([
  'event Transfer(address indexed from, address indexed to, uint256 value)',
  'function symbol() view returns (string)',
  'function decimals() view returns (uint8)',
]);

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function priceFromSqrtX96(sqrtPriceX96, dec0, dec1) {
  // price = (sqrtPriceX96 / 2^96)^2, in token1 per token0, raw units.
  const Q96 = 2n ** 96n;
  const num = sqrtPriceX96 * sqrtPriceX96;
  // Scale to keep precision: price * 1e18
  const scaled = (num * 10n ** 18n) / (Q96 * Q96);
  const raw = Number(scaled) / 1e18;
  return raw * 10 ** (dec0 - dec1);
}

(async () => {
  const p = new ethers.JsonRpcProvider(RPC);

  const rcpt = await p.getTransactionReceipt(TX);
  if (!rcpt) {
    console.error('receipt not found (not mined yet, or wrong network)');
    process.exit(1);
  }
  await sleep(500);
  const tx = await p.getTransaction(TX);

  console.log('=== transaction ===');
  console.log('status     ', rcpt.status === 1 ? 'SUCCESS' : 'REVERTED');
  console.log('block      ', rcpt.blockNumber);
  console.log('from       ', rcpt.from);
  console.log('to         ', rcpt.to);
  console.log('to is PM   ', rcpt.to.toLowerCase() === POSITION_MANAGER.toLowerCase());
  console.log('value ETH  ', ethers.formatEther(tx.value));
  console.log('gas used   ', rcpt.gasUsed.toString());
  console.log('logs       ', rcpt.logs.length);

  let init = null;
  const mods = [];
  const nftTransfers = [];
  const tokenTransfers = [];

  for (const log of rcpt.logs) {
    if (log.address.toLowerCase() === POOL_MANAGER.toLowerCase()) {
      try {
        const parsed = iface.parseLog(log);
        if (parsed.name === 'Initialize') init = parsed;
        if (parsed.name === 'ModifyLiquidity') mods.push(parsed);
      } catch (_) {}
      continue;
    }
    if (log.address.toLowerCase() === POSITION_MANAGER.toLowerCase()) {
      try {
        const parsed = iface.parseLog(log);
        if (parsed.name === 'Transfer') nftTransfers.push(parsed);
      } catch (_) {}
      continue;
    }
    try {
      const parsed = erc20.parseLog(log);
      if (parsed.name === 'Transfer') {
        tokenTransfers.push({ token: log.address, ...parsed.args });
      }
    } catch (_) {}
  }

  if (!init) {
    console.log('\nNO Initialize EVENT. This transaction did not create a pool.');
  } else {
    const [id, c0, c1, fee, tickSpacing, hooks, sqrtPriceX96, tick] = init.args;
    console.log('\n=== pool initialized ===');
    console.log('PoolId     ', id);
    console.log('currency0  ', c0, c0 === ethers.ZeroAddress ? '(native ETH)' : '');
    console.log('currency1  ', c1);
    console.log('fee        ', fee.toString(), '=', Number(fee) / 10000 + '%');
    console.log('tickSpacing', tickSpacing.toString());
    console.log('hooks      ', hooks, hooks === ethers.ZeroAddress ? '(none)' : '(HOOK PRESENT)');
    console.log('sqrtPriceX96', sqrtPriceX96.toString());
    console.log('init tick  ', tick.toString());

    let dec0 = 18;
    let dec1 = 18;
    let sym0 = 'ETH';
    let sym1 = '?';
    if (c0 !== ethers.ZeroAddress) {
      await sleep(500);
      const t0 = new ethers.Contract(c0, erc20.fragments, p);
      dec0 = Number(await t0.decimals());
      sym0 = await t0.symbol();
    }
    if (c1 !== ethers.ZeroAddress) {
      await sleep(500);
      const t1 = new ethers.Contract(c1, erc20.fragments, p);
      dec1 = Number(await t1.decimals());
      await sleep(300);
      sym1 = await t1.symbol();
    }
    console.log(`decimals   ${sym0}=${dec0} ${sym1}=${dec1}`);

    const price = priceFromSqrtX96(sqrtPriceX96, dec0, dec1);
    console.log('\n=== price ===');
    console.log(`1 ${sym0} = ${price.toLocaleString('en-US', { maximumFractionDigits: 4 })} ${sym1}`);
    console.log(`1 ${sym1} = ${(1 / price).toExponential(6)} ${sym0}`);
  }

  console.log('\n=== liquidity ===');
  if (mods.length === 0) {
    console.log('NO ModifyLiquidity EVENT. Pool created but NOT funded.');
  }
  for (const m of mods) {
    const [id, sender, tickLower, tickUpper, liquidityDelta, salt] = m.args;
    console.log('poolId     ', id);
    console.log('sender     ', sender);
    console.log('tickLower  ', tickLower.toString());
    console.log('tickUpper  ', tickUpper.toString());
    console.log('liquidity  ', liquidityDelta.toString());
    console.log('salt       ', salt);
  }

  console.log('\n=== position NFT ===');
  for (const t of nftTransfers) {
    console.log(`tokenId ${t.args[2].toString()}  ${t.args[0]} -> ${t.args[1]}`);
  }

  console.log('\n=== token transfers ===');
  for (const t of tokenTransfers) {
    let sym = t.token;
    let dec = 18;
    try {
      await sleep(400);
      const c = new ethers.Contract(t.token, erc20.fragments, p);
      sym = await c.symbol();
      await sleep(300);
      dec = Number(await c.decimals());
    } catch (_) {}
    console.log(`${sym}: ${t.from} -> ${t.to} ${ethers.formatUnits(t.value, dec)}`);
  }
})().catch((e) => {
  console.error('FAILED:', e.message);
  process.exit(1);
});
