#!/usr/bin/env python3
"""YAML work for scripts/launch-plan.sh: read, merge, and diff launch configs.

Kept out of the shell script because merging into a generated config is the part
that must not be approximate. `matrixd -init` writes a node's own secrets, and a
launch has to ADD the shared decisions to that file without disturbing anything
it generated - which sed cannot do safely and a full rewrite would lose.

The diff is here for the same reason. The runbook asks for a diff between the
node configs showing only each node's own identity differing; done by eye across
three files, that is the check that misses a transposed character in a validator
id, and a network whose genesis names an account that never validates comes up
holding nothing.
"""

import json
import sys

try:
    import yaml
except ImportError:  # pragma: no cover - the script says what to do about it
    sys.stderr.write("this needs PyYAML: pip install pyyaml\n")
    sys.exit(2)


REQUIRED_TOP = ("chain_id", "genesis_file", "round_timeout", "epoch_length")
REQUIRED_NODE = ("name", "consensus_id")

# The fields every node must agree on exactly. Consensus is a set of nodes
# holding the SAME rules; a node that disagrees on any of these is not a
# validator of this chain, it is a validator of a different one that happens to
# be running nearby.
CONSENSUS_CRITICAL = (
    ("consensus", "chain_id"),
    ("consensus", "validators"),
    ("consensus", "round_timeout"),
    ("consensus", "epoch_length"),
    ("genesis",),
)


def die(message):
    sys.stderr.write(f"{message}\n")
    sys.exit(1)


def load_plan(path):
    with open(path, "r", encoding="utf-8") as handle:
        plan = yaml.safe_load(handle)
    if not isinstance(plan, dict):
        die(f"{path} is not a mapping")

    for field in REQUIRED_TOP:
        if plan.get(field) in (None, ""):
            die(f"{path} is missing {field}")

    if not isinstance(plan.get("chain_id"), int):
        die("chain_id must be a plain integer, not a string or a hex literal")

    nodes = plan.get("nodes")
    if not isinstance(nodes, list) or len(nodes) < 3:
        # Not a style rule. A quorum needs more than one node to mean anything,
        # and production preflight refuses fewer than three, so catching it here
        # saves generating configs that cannot launch.
        die("nodes must be a list of at least 3 validators; production refuses fewer")

    seen_names, seen_ids = set(), set()
    for i, node in enumerate(nodes):
        if not isinstance(node, dict):
            die(f"nodes[{i}] is not a mapping")
        for field in REQUIRED_NODE:
            if not node.get(field):
                die(f"nodes[{i}] is missing {field}")
        name, cid = node["name"], str(node["consensus_id"])
        if name in seen_names:
            die(f"two nodes are both called {name!r}")
        # The failure this catches: pasting the same identity twice while
        # collecting them from three hosts. The validator list then names two
        # accounts instead of three, and the quorum is smaller than it looks.
        if cid in seen_ids:
            die(f"two nodes share consensus_id {cid!r}; each host has its own identity")
        seen_names.add(name)
        seen_ids.add(cid)
    return plan


def warn_relative_storage(config_path, cfg):
    """A relative storage.path makes a node's identity depend on the directory
    the command was run from.

    Found by this script's own duplicate-identity check, on this script's own
    test harness. `storage.path: ./data` is the generated default, and reading
    three nodes' identities from a parent directory resolves all three to one
    store - so all three come back with the SAME id, which then goes into the
    validator list three times. The quorum is smaller than it looks, and nothing
    about the config says so.

    The runbook warns about running -init-identities against the wrong config;
    this is the variant where the config is right and the working directory is
    not.
    """
    path = (cfg.get("storage") or {}).get("path", "")
    if path and not str(path).startswith("/"):
        sys.stderr.write(
            f"   note: {config_path} has a relative storage.path ({path!r}).\n"
            "         A node's identity lives in that store, so it depends on the\n"
            "         directory the command runs from. Use an absolute path.\n"
        )


def cmd_read(path):
    print(json.dumps(load_plan(path)))


def cmd_merge(plan_path, node_name, config_path):
    plan = load_plan(plan_path)
    node = next((n for n in plan["nodes"] if n["name"] == node_name), None)
    if node is None:
        die(f"no node called {node_name!r} in {plan_path}")

    with open(config_path, "r", encoding="utf-8") as handle:
        cfg = yaml.safe_load(handle) or {}

    with open(plan["genesis_file"], "r", encoding="utf-8") as handle:
        genesis_doc = yaml.safe_load(handle) or {}
    genesis = genesis_doc.get("genesis", genesis_doc)
    if not isinstance(genesis, dict) or not genesis:
        die(f"{plan['genesis_file']} carries no genesis block")

    consensus = cfg.setdefault("consensus", {})
    consensus["chain_id"] = plan["chain_id"]
    consensus["validators"] = [str(n["consensus_id"]) for n in plan["nodes"]]
    consensus["round_timeout"] = str(plan["round_timeout"])
    consensus["epoch_length"] = plan["epoch_length"]

    stake = consensus.setdefault("stake", {})
    stake["enabled"] = True
    if plan.get("min_bond"):
        stake["min_bond"] = plan["min_bond"]
    if plan.get("bond"):
        stake["bond"] = plan["bond"]

    cfg["genesis"] = genesis

    # The absolute path this host will actually run from, when the plan names
    # one. A node's identity lives in its store, so a relative path makes that
    # identity depend on the directory the command was run from - see
    # warn_relative_storage, which found exactly that.
    if node.get("storage_path"):
        cfg.setdefault("storage", {})["path"] = node["storage_path"]

    # Per-node, and deliberately not shared. An endpoint is the address BUYERS
    # dial; a node cannot see its own public address, so it has to be told.
    if node.get("endpoint"):
        cfg.setdefault("market", {})["endpoint"] = node["endpoint"]

    # Every OTHER node's peer address. A node does not dial itself, and listing
    # its own address as a bootstrap peer is a loop that looks like a config.
    peers = [n["peer"] for n in plan["nodes"] if n.get("peer") and n["name"] != node_name]
    if peers:
        cfg.setdefault("p2p", {})["bootstrap_peers"] = peers

    warn_relative_storage(config_path, cfg)

    with open(config_path, "w", encoding="utf-8") as handle:
        yaml.safe_dump(cfg, handle, sort_keys=False, default_flow_style=False)


def dig(cfg, path):
    cur = cfg
    for part in path:
        if not isinstance(cur, dict) or part not in cur:
            return None
        cur = cur[part]
    return cur


def cmd_diff(out_dir):
    import pathlib

    configs = sorted(pathlib.Path(out_dir).glob("*/config.yaml"))
    if len(configs) < 2:
        die(f"found {len(configs)} configs under {out_dir}; nothing to compare")

    loaded = []
    for path in configs:
        with open(path, "r", encoding="utf-8") as handle:
            loaded.append((path.parent.name, yaml.safe_load(handle) or {}))

    reference_name, reference = loaded[0]
    problems = []
    for name, cfg in loaded[1:]:
        for path in CONSENSUS_CRITICAL:
            want, got = dig(reference, path), dig(cfg, path)
            if want != got:
                label = ".".join(path)
                problems.append(f"{name} disagrees with {reference_name} on {label}")

    # The other half of the check: every node's admin key must be its OWN. A
    # config copied between hosts rather than generated per host hands one
    # credential the run of the whole network, and it looks identical to a
    # correct launch from the outside.
    keys = {}
    for name, cfg in loaded:
        for entry in (dig(cfg, ("security", "api_keys")) or []):
            key = entry.get("key")
            if not key:
                continue
            if key in keys:
                problems.append(f"{name} and {keys[key]} share an API key; each node generates its own")
            keys[key] = name

    if problems:
        for problem in problems:
            sys.stderr.write(f"   {problem}\n")
        sys.exit(1)


def main():
    if len(sys.argv) < 2:
        die("usage: launch_plan.py read|merge|diff ...")
    command = sys.argv[1]
    if command == "read" and len(sys.argv) == 3:
        cmd_read(sys.argv[2])
    elif command == "merge" and len(sys.argv) == 5:
        cmd_merge(sys.argv[2], sys.argv[3], sys.argv[4])
    elif command == "diff" and len(sys.argv) == 3:
        cmd_diff(sys.argv[2])
    else:
        die("usage: launch_plan.py read <plan> | merge <plan> <node> <config> | diff <dir>")


if __name__ == "__main__":
    main()
