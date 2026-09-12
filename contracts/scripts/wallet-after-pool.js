// Reports what the wallet holds after the pool was funded, so the amount that
// actually went in can be checked against the amount that was meant to and the
// remaining gas budget can be seen at a glance.
//
// Requests are issued one at a time with a pause between them: the public Base
// RPC rate-limits bundled calls and answers with a bare "missing revert data",
// which reads like a contract failure and is not one.

const { ethers } = require('ethers');

const RPC = process.env.RPC_URL || 'https://mainnet.base.org';
const WALLET = '0x856E3FFf84A5e833420b43cec0B4e13c16779817';
const WMATRIX = '0x0ec1C40829B3A5C2349Afa8C4Da6dC8B727fC87c';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const p = new ethers.JsonRpcProvider(RPC);
  const t = new ethers.Contract(
    WMATRIX,
    [
      'function balanceOf(address) view returns (uint256)',
      'function totalSupply() view returns (uint256)',
      'function decimals() view returns (uint8)',
      'function symbol() view returns (string)',
    ],
    p
  );

  const eth = await p.getBalance(WALLET);
  await sleep(700);
  const bal = await t.balanceOf(WALLET);
  await sleep(700);
  const supply = await t.totalSupply();

  console.log('wallet     ', WALLET);
  console.log('ETH        ', ethers.formatEther(eth));
  console.log('wMATRIX    ', ethers.formatUnits(bal, 18));
  console.log('totalSupply', ethers.formatUnits(supply, 18));
})().catch((e) => {
  console.error('FAILED:', e.message);
  process.exit(1);
});
