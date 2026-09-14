#!/usr/bin/env python3
"""Config reading for scripts/gpu-provider-preflight.sh.

Kept out of the shell script because the answers depend on YAML structure -
whether a key is absent, present-and-empty, or present-and-false are three
different configs with three different failures, and grep cannot tell them
apart. `participate_in_open_set` unset is not the same as `false`, and a
provider that gets that wrong sees a node demanding a bond it never meant to
post.

This reads and reports. It writes nothing, starts nothing, and reaches no
network; the shell script does the probing that needs the host.
"""

import json
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover - the script says what to do about it
    sys.stderr.write("this needs PyYAML: pip install pyyaml\n")
    sys.exit(2)


LOOPBACK = re.compile(r"^(127\.\d+\.\d+\.\d+|localhost|\[?::1\]?)$")
ETH_ACCOUNT = re.compile(r"^eth:0x[0-9a-f]{40}$")

# 60s is the inference package default, and it is the wrong one for a model
# running on a GPU you own: a few thousand tokens routinely runs longer, and the
# cap fires AFTER the work is done.
LOCAL_TIMEOUT_FLOOR_SECONDS = 60


def die(message):
    sys.stderr.write(f"{message}\n")
    sys.exit(2)


def host_of(addr):
    """The host half of an addr, tolerating IPv6 brackets and a bare port."""
    if not isinstance(addr, str) or addr == "":
        return ""
    if addr.startswith("["):
        return addr[: addr.find("]") + 1] if "]" in addr else addr
    return addr.rsplit(":", 1)[0] if ":" in addr else addr


def is_loopback(addr):
    return bool(LOOPBACK.match(host_of(addr)))


def duration_seconds(value):
    """Go's duration spelling, as far as a config ever uses it. None if unset."""
    if value is None or value == "":
        return None
    if isinstance(value, (int, float)):
        return float(value)
    text = str(value).strip()
    total, number = 0.0, ""
    units = {"ns": 1e-9, "us": 1e-6, "ms": 1e-3, "s": 1.0, "m": 60.0, "h": 3600.0}
    i = 0
    while i < len(text):
        if text[i].isdigit() or text[i] == ".":
            number += text[i]
            i += 1
            continue
        unit = text[i : i + 2]
        if unit not in units:
            unit = text[i : i + 1]
            if unit not in units:
                return None
        if number == "":
            return None
        total += float(number) * units[unit]
        number = ""
        i += len(unit)
    return total if number == "" else None


class Report:
    """Findings in the order a provider hits them, not by severity."""

    def __init__(self):
        self.findings = []

    def add(self, level, label, detail):
        self.findings.append({"level": level, "label": label, "detail": detail})

    def ok(self, label, detail=""):
        self.add("PASS", label, detail)

    def warn(self, label, detail):
        self.add("WARN", label, detail)

    def bad(self, label, detail):
        self.add("FAIL", label, detail)


def check_joining(cfg, r):
    """Can this box join the network at all - the failures that look like others.

    A wrong genesis is the expensive one. It is applied once and the fact is
    recorded, so it cannot be corrected in place, and what it looks like is a
    node that connects to its peers and never advances.
    """
    genesis = cfg.get("genesis")
    if not genesis:
        r.bad(
            "genesis",
            "missing. A provider replays the chain from height zero, so without "
            "the network's own genesis it computes a different state root and "
            "refuses every block. Copy it verbatim off a node already running.",
        )
    elif not genesis.get("allocations"):
        r.bad("genesis.allocations", "empty. This is not the network's genesis.")
    else:
        r.ok("genesis", f"{len(genesis['allocations'])} allocations")

    consensus = cfg.get("consensus") or {}
    validators = consensus.get("validators") or []
    if not validators:
        r.bad(
            "consensus.validators",
            "empty. Each replayed block is checked against the validator set as "
            "it stood at that height, so this has to be the GENESIS set - not "
            "the set validating today.",
        )
    elif len(set(validators)) != len(validators):
        r.bad("consensus.validators", "contains a duplicate")
    else:
        r.ok("consensus.validators", f"{len(validators)} entries")

    chain_id = consensus.get("chain_id") or 0
    if not chain_id:
        r.bad("consensus.chain_id", "unset. It is inside every wallet signature.")
    else:
        r.ok("consensus.chain_id", str(chain_id))

    # Unset is not false. Unset leaves the node trying to bond, and production
    # preflight then demands a bond the provider never meant to post - which
    # reads as a broken config rather than a decision nobody made.
    participates = consensus.get("participate_in_open_set", None)
    if participates is None:
        r.bad(
            "consensus.participate_in_open_set",
            "unset, which means the node tries to bond into the validator set. "
            "A provider sells work; write it as false.",
        )
    elif participates:
        r.warn(
            "consensus.participate_in_open_set",
            "true. This box will bond and validate as well as sell. That is a "
            "decision, not a default - it needs stake and it needs uptime.",
        )
    else:
        r.ok("consensus.participate_in_open_set", "false, a peer that sells")

    peers = (cfg.get("network") or {}).get("bootstrap_peers") or []
    if not peers:
        r.bad("network.bootstrap_peers", "empty. Nothing to join.")
    else:
        malformed = [p for p in peers if "/p2p/" not in str(p)]
        if malformed:
            r.bad(
                "network.bootstrap_peers",
                f"{len(malformed)} entry without /p2p/<peer id>: {malformed[0]}",
            )
        else:
            r.ok("network.bootstrap_peers", f"{len(peers)} peers")


def check_exposure(cfg, r):
    """What is reachable, and by whom."""
    admin = (cfg.get("admin") or {}).get("addr", "")
    if admin and not is_loopback(admin):
        r.bad(
            "admin.addr",
            f"{admin} is not loopback. The admin API's key can move funds; the "
            "only thing between it and the internet would be a firewall rule. "
            "Bind 127.0.0.1 and reach it over an SSH tunnel.",
        )
    else:
        r.ok("admin.addr", admin or "unset")

    listen = (cfg.get("network") or {}).get("listen_addr", "")
    if is_loopback(listen) or "/ip4/127." in str(listen) or "/ip6/::1" in str(listen):
        r.bad(
            "network.listen_addr",
            f"{listen} is loopback, so no peer can reach this node. On a cloud "
            "instance bind the wildcard and advertise the public address.",
        )
    elif not listen:
        r.warn("network.listen_addr", "unset")
    else:
        r.ok("network.listen_addr", listen)

    security = cfg.get("security") or {}
    if not security.get("enable_acls"):
        r.bad(
            "security.enable_acls",
            "off. Every surface answers without a key, including the one that "
            "moves funds.",
        )
    else:
        r.ok("security.enable_acls", "on")

    keys = security.get("api_keys") or []
    buying = [k for k in keys if (k or {}).get("account")]
    if keys and not buying:
        r.warn(
            "security.api_keys",
            "no key carries an `account`. Such a key drives every other surface "
            "and cannot buy inference - which surfaces as a 401 from a key that "
            "works everywhere else. Your own end-to-end test needs one.",
        )
    elif buying:
        r.ok("security.api_keys", f"{len(buying)} of {len(keys)} can buy")

    connect = cfg.get("connect") or {}
    if not connect.get("signed_writes"):
        r.warn(
            "connect.signed_writes",
            "false. RunInferenceJob then takes `buyer` as a bare string, so only "
            "accounts whose keys this node custodies can pay. Turn it on to sell "
            "to buyers who are not you.",
        )
    else:
        r.ok("connect.signed_writes", "true, buyers sign for themselves")

    eth_rpc = cfg.get("eth_rpc") or {}
    if eth_rpc.get("addr") and not (cfg.get("consensus") or {}).get("chain_id"):
        r.bad(
            "eth_rpc.addr",
            "set with no consensus.chain_id. The node refuses to start the "
            "endpoint, and it is right to: a wallet would connect, show a "
            "balance, and fail every send.",
        )


def check_backends(cfg, r):
    """The listing itself: what is sold, for how much, and who is paid."""
    inference = cfg.get("inference") or {}

    echo = inference.get("echo_provider")
    if echo:
        r.bad(
            "inference.echo_provider",
            f'set to "{echo}". That is a GPU-free stub answering paying prompts. '
            "Clear it.",
        )
    else:
        r.ok("inference.echo_provider", "cleared")

    backends = inference.get("backends") or []
    if not backends:
        r.bad(
            "inference.backends",
            "empty, and a node with no backend still starts. Nothing is for sale.",
        )
        return

    for index, backend in enumerate(backends):
        backend = backend or {}
        name = backend.get("id") or f"backends[{index}]"
        kind = (backend.get("kind") or "").lower()
        base_url = backend.get("base_url") or ""
        local = is_loopback(base_url.split("//")[-1]) if base_url else False

        # The id IS the payout account. settle credits it directly, and nothing
        # later asks whether anyone holds its key.
        if backend.get("id") != (backend.get("id") or "").lower():
            r.bad(f"{name}: id", "not lowercase. Accounts are lowercased, so this "
                                 "is a different account from the one you meant.")
        elif not ETH_ACCOUNT.match(backend.get("id") or ""):
            r.warn(
                f"{name}: id",
                "is not an eth:0x... address. The id IS the account revenue is "
                "paid into. Use the wallet address you already hold, or the "
                "proceeds land somewhere you cannot spend from.",
            )
        else:
            r.ok(f"{name}: id", "a wallet address you can spend from")

        if kind == "echo":
            r.bad(f"{name}: kind", "echo answers paying prompts with a stub")
        elif kind not in ("openai", "local-http"):
            r.bad(f"{name}: kind", f'"{kind}" is not a backend kind')
        else:
            r.ok(f"{name}: kind", kind)

        if not base_url:
            r.warn(f"{name}: base_url", "empty, which for `openai` means the "
                                        "public OpenAI API")
        elif not local:
            r.warn(
                f"{name}: base_url",
                f"{base_url} is not loopback. Fine if you are reselling a "
                "vendor's API. If this is your own model server, it has no auth "
                "worth exposing and no metering: that is free inference for "
                "anyone who finds the port.",
            )
        else:
            r.ok(f"{name}: base_url", base_url)

        if kind == "openai" and not backend.get("api_key_env"):
            r.bad(
                f"{name}: api_key_env",
                "unset. The openai backend fails construction on an empty key, "
                "so the node refuses to start rather than advertising capacity "
                "it cannot reach.",
            )

        if not backend.get("models"):
            r.bad(f"{name}: models", "empty. Buyers match on this string.")
        else:
            r.ok(f"{name}: models", ", ".join(str(m) for m in backend["models"]))

        if not backend.get("capacity"):
            r.bad(f"{name}: capacity", "zero, so every reservation is refused")
        else:
            r.ok(f"{name}: capacity", f"{backend['capacity']} tokens in flight")

        price = backend.get("price_per_unit") or 0
        cost = backend.get("cost_per_unit") or 0
        if price and cost:
            r.bad(
                f"{name}: price",
                "price_per_unit and cost_per_unit are mutually exclusive modes",
            )
        elif price:
            r.ok(f"{name}: price_per_unit", f"{price} base units per token, gross")
        elif cost:
            if not backend.get("markup_basis_points"):
                r.warn(f"{name}: markup_basis_points", "zero, so you sell at cost")
            r.ok(f"{name}: cost_per_unit", f"{cost} plus markup, grossed up for fee")
        else:
            r.bad(
                f"{name}: price",
                "neither price_per_unit nor cost_per_unit is set. Provider "
                "emission is 0 per block, so settlement is the whole of your "
                "revenue and this sells the GPU for nothing.",
            )

        timeout = duration_seconds(backend.get("request_timeout"))
        if local and (timeout is None or timeout <= LOCAL_TIMEOUT_FLOOR_SECONDS):
            r.warn(
                f"{name}: request_timeout",
                f"{backend.get('request_timeout') or 'unset (60s)'} for a model "
                "on your own GPU. A few thousand tokens routinely runs longer, "
                "and the cap fires after the work is done: electricity spent, "
                "nothing sold.",
            )
        elif timeout:
            r.ok(f"{name}: request_timeout", str(backend["request_timeout"]))

        if backend.get("health_check") is False:
            r.warn(
                f"{name}: health_check",
                "off. A crashed model server keeps winning routing decisions and "
                "taking reservations it cannot serve.",
            )
        else:
            path = backend.get("health_check_path") or "/v1/models"
            if local and path == "/v1/models":
                r.warn(
                    f"{name}: health_check_path",
                    "/v1/models can answer from a list built at startup even when "
                    "the engine is wedged. Point it at /health.",
                )
            else:
                r.ok(f"{name}: health_check_path", path)


def probe_plan(cfg):
    """What the shell has to reach out and check for itself."""
    inference = cfg.get("inference") or {}
    backends = []
    for backend in inference.get("backends") or []:
        backend = backend or {}
        if (backend.get("kind") or "").lower() not in ("openai", "local-http"):
            continue
        backends.append(
            {
                "id": backend.get("id") or "",
                "kind": (backend.get("kind") or "").lower(),
                "base_url": backend.get("base_url") or "",
                "api_key_env": backend.get("api_key_env") or "",
                "model": (backend.get("models") or [""])[0],
                "health_check_path": backend.get("health_check_path") or "",
            }
        )
    return {
        "backends": backends,
        "bootstrap_peers": [
            str(p) for p in (cfg.get("network") or {}).get("bootstrap_peers") or []
        ],
        "listen_addr": (cfg.get("network") or {}).get("listen_addr") or "",
    }


def main():
    if len(sys.argv) != 2:
        die("usage: gpu_provider_preflight.py <config.yaml>")
    try:
        with open(sys.argv[1], "r", encoding="utf-8") as handle:
            cfg = yaml.safe_load(handle)
    except FileNotFoundError:
        die(f"no such config: {sys.argv[1]}")
    except yaml.YAMLError as err:
        die(f"the config is not valid YAML: {err}")
    if not isinstance(cfg, dict):
        die("the config is not a YAML mapping")

    report = Report()
    check_joining(cfg, report)
    check_exposure(cfg, report)
    check_backends(cfg, report)
    json.dump({"findings": report.findings, "probe": probe_plan(cfg)}, sys.stdout)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
