#!/usr/bin/env python3
"""YAML work for scripts/first-party-provider.sh.

Separate from the shell for the same reason as launch_plan.py: the merge adds
launch values to a config `matrixd -init` generated, and must not disturb the
secrets it wrote. A rewrite loses them; sed cannot do it safely.
"""

import json
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    sys.stderr.write("this needs PyYAML: pip install pyyaml\n")
    sys.exit(2)


REQUIRED = ("network_config", "bootstrap_peers", "provider", "maintainer_wallet")
REQUIRED_PROVIDER = ("operator", "payout_account", "endpoint", "models", "vllm_url", "storage_path")

# The protocol's own ceiling, mirrored here so the refusal comes before a wallet
# is touched rather than after. See MaxOperatorAttestationValidity.
MAX_VALID_HOURS = 30 * 24


def die(message):
    sys.stderr.write(f"{message}\n")
    sys.exit(1)


def load(path):
    with open(path, "r", encoding="utf-8") as handle:
        plan = yaml.safe_load(handle)
    if not isinstance(plan, dict):
        die(f"{path} is not a mapping")

    for field in REQUIRED:
        if plan.get(field) in (None, "", []):
            die(f"{path} is missing {field}")

    provider = plan["provider"]
    if not isinstance(provider, dict):
        die("provider must be a mapping")
    for field in REQUIRED_PROVIDER:
        if provider.get(field) in (None, "", []):
            die(f"provider is missing {field}")

    if not str(provider["storage_path"]).startswith("/"):
        # A node's identity lives in its store, and the attestation is bound to
        # that identity. A relative path resolves against whatever directory the
        # command happened to run from, so the id - and therefore the badge -
        # depends on where somebody stood.
        die("provider.storage_path must be absolute; a node's identity depends on it")

    if not isinstance(provider["models"], list):
        die("provider.models must be a list of served model names")

    plan.setdefault("attestation_valid_for", "336h")
    raw = str(plan["attestation_valid_for"])
    if not raw.endswith("h"):
        die("attestation_valid_for must be given in hours, e.g. 336h")
    try:
        hours = float(raw[:-1])
    except ValueError:
        die(f"attestation_valid_for {raw!r} is not a duration")
    if hours <= 0:
        die("attestation_valid_for must be positive")
    if hours > MAX_VALID_HOURS:
        die(
            f"attestation_valid_for is {raw}; the protocol refuses anything beyond "
            f"{MAX_VALID_HOURS}h, because a badge nobody has to renew outlives the "
            "arrangement it describes"
        )

    return plan


# What a node must AGREE ON to be on the same chain, copied wholesale from a
# node that is already on it.
#
# Found by running this against a live network: the first version set chain_id
# and nothing else, so `matrixd -init`'s own generated genesis and single-entry
# validator list survived. The provider came up, registered its backend, and ran
# a private chain of ONE - announcing to nobody, and verifying its own badge
# against a maintainer only it had ever named. Nothing errored.
#
# Retyping these is the same bug with extra steps, which is why the input is a
# path to a real config rather than six fields to copy by hand.
INHERITED_CONSENSUS = (
    "chain_id",
    "validators",
    # The account every reader checks an attestation against. A provider on a
    # chain naming a different maintainer is a provider whose badge is refused.
    "maintainer_account",
    "maintainer_fee_share_basis_points",
    "fee_basis_points",
    "round_timeout",
    "epoch_length",
    "membership_mode",
)

# Copied from the network's stake block, minus `bond`. That one is a VALIDATOR's
# bond: the node posts it to join the ordering set, and a GPU box carrying it
# spends its life asking to join a set it was configured to stay out of. The
# seller's own market stake is a different thing entirely, posted from the
# payout wallet with `matrix stake bond`.
INHERITED_STAKE = ("enabled", "min_bond", "unbonding_period", "bond_residency")


def inherit_network(cfg, network_config_path):
    """Copy the consensus-critical settings from a node already on the network."""
    with open(network_config_path, "r", encoding="utf-8") as handle:
        src = yaml.safe_load(handle) or {}
    src_consensus = src.get("consensus") or {}
    if not src_consensus.get("chain_id"):
        die(f"{network_config_path} names no consensus.chain_id; it is not a node on a running network")
    if not src_consensus.get("validators"):
        die(f"{network_config_path} names no consensus.validators")

    consensus = cfg.setdefault("consensus", {})
    for field in INHERITED_CONSENSUS:
        if field in src_consensus:
            consensus[field] = src_consensus[field]

    src_stake = src_consensus.get("stake") or {}
    stake = consensus.setdefault("stake", {})
    for field in INHERITED_STAKE:
        if field in src_stake:
            stake[field] = src_stake[field]

    genesis = src.get("genesis")
    if not genesis:
        die(f"{network_config_path} carries no genesis block")
    cfg["genesis"] = genesis

    # Deliberately NOT copied: security.api_keys, storage.path, every listen
    # address, and the market endpoint. Those are the other node's credentials
    # and the other node's addresses, and carrying them over is how one
    # credential ends up running the whole network.
    return src_consensus["chain_id"]


def cmd_read(path):
    plan = load(path)
    # The chain id is not in the plan; it comes from the network config, which is
    # the point. Resolve it here so the shell can print what it is joining.
    with open(plan["network_config"], "r", encoding="utf-8") as handle:
        src = yaml.safe_load(handle) or {}
    plan["chain_id"] = (src.get("consensus") or {}).get("chain_id")
    plan["maintainer_account"] = (src.get("consensus") or {}).get("maintainer_account")
    print(json.dumps(plan))


def cmd_merge(plan_path, config_path):
    plan = load(plan_path)
    p = plan["provider"]

    with open(config_path, "r", encoding="utf-8") as handle:
        cfg = yaml.safe_load(handle) or {}

    net = cfg.setdefault("network", {})
    net["listen_addr"] = "/ip4/0.0.0.0/tcp/9000"
    net["bootstrap_peers"] = list(plan["bootstrap_peers"])

    inherit_network(cfg, plan["network_config"])
    consensus = cfg["consensus"]
    # A provider SELLS COMPUTE; it does not order blocks. Leaving this false is
    # what keeps a GPU box out of the validator set, so it needs no bonded stake
    # and no attestor keystore.
    consensus["participate_in_open_set"] = False

    # And the validator bond has to go with it.
    #
    # Found by running this: `matrixd -init` writes a consensus.stake.bond, and
    # the node tries to post it whatever participate_in_open_set says. A GPU box
    # built from the generated config therefore spends its life asking to join a
    # validator set it was configured to stay out of, logging "holds nothing"
    # against an account nobody funded, every round, forever.
    #
    # This is NOT the seller's market stake. That one makes a LISTING cost
    # capital and is posted from the payout wallet with `matrix stake bond`;
    # this one is a validator's bond, and a provider has no use for it.
    consensus.setdefault("stake", {})["bond"] = 0

    cfg.setdefault("storage", {})["path"] = p["storage_path"]

    # Loopback only. The admin API's key can move funds.
    cfg.setdefault("admin", {})["addr"] = "127.0.0.1:9090"

    connect = cfg.setdefault("connect", {})
    connect["addr"] = "0.0.0.0:9093"
    connect["public_reads"] = True
    # Opens signature-authorised writes, and ALSO makes RunInferenceJob require
    # the buyer's signed RunAuthorization. Without it `buyer` is just a string
    # and anyone could name a funded account and never sign for it.
    connect["signed_writes"] = True

    inference = cfg.setdefault("inference", {})
    inference["addr"] = "0.0.0.0:9092"
    # `matrixd -init` registers a GPU-free echo backend so a fresh node can
    # fulfill inference without hardware. On a real provider that is a listing
    # that answers paying prompts with a stub. It must also be cleared because a
    # backend id equal to echo_provider is refused at startup.
    inference["echo_provider"] = ""
    inference["backends"] = [
        {
            "id": p["payout_account"],
            "kind": "openai",
            "base_url": p["vllm_url"],
            # NAMES the variable; never carries the key. An empty variable fails
            # construction at startup, which beats advertising capacity that
            # cannot be served.
            "api_key_env": "MATRIX_VLLM_API_KEY",
            "models": list(p["models"]),
            "price_per_unit": p.get("price_per_unit", 5),
            "capacity": p.get("capacity", 1_000_000),
            "request_timeout": p.get("request_timeout", "10m"),
            # /health reports on the inference ENGINE. The default /v1/models can
            # still answer from a list built at startup while the engine is
            # wedged, which is the case this probe exists to catch.
            "health_check_path": "/health",
            "health_check_interval": "30s",
        }
    ]

    cfg.setdefault("market", {})["endpoint"] = p["endpoint"]

    with open(config_path, "w", encoding="utf-8") as handle:
        yaml.safe_dump(cfg, handle, sort_keys=False, default_flow_style=False)


def cmd_attach(config_path, attestation_path):
    with open(config_path, "r", encoding="utf-8") as handle:
        cfg = yaml.safe_load(handle) or {}
    backends = (cfg.get("inference") or {}).get("backends") or []
    if not backends:
        die("the config declares no backends, so there is nothing to vouch for")

    with open(attestation_path, "r", encoding="utf-8") as handle:
        att = json.load(handle)

    # The node refuses an attestation whose provider id does not match the
    # backend it is attached to. Catching it here says which two values
    # disagree, instead of a node that will not start.
    if att.get("provider_id") and att["provider_id"] != backends[0]["id"]:
        die(
            f"the attestation is for {att['provider_id']} but this backend sells as "
            f"{backends[0]['id']}; a node refuses that pairing"
        )

    backends[0]["attestation"] = attestation_path
    with open(config_path, "w", encoding="utf-8") as handle:
        yaml.safe_dump(cfg, handle, sort_keys=False, default_flow_style=False)


def main():
    if len(sys.argv) >= 3 and sys.argv[1] == "read":
        cmd_read(sys.argv[2])
    elif len(sys.argv) == 4 and sys.argv[1] == "merge":
        cmd_merge(sys.argv[2], sys.argv[3])
    elif len(sys.argv) == 4 and sys.argv[1] == "attach":
        cmd_attach(sys.argv[2], sys.argv[3])
    else:
        die("usage: first_party_provider.py read <plan> | merge <plan> <config> | attach <config> <attestation>")


if __name__ == "__main__":
    main()
