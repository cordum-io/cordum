#!/usr/bin/env python3
"""Exercise the shipped recovery shell with disposable files and fake Redis/NATS."""
import os
import hashlib
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[2]
BASH = os.environ.get("BASH") or shutil.which("bash")
if os.name == "nt":
    BASH = r"C:\Program Files\Git\bin\bash.exe"


def shellpath(path):
    value = str(path).replace("\\", "/")
    return "/" + value[0].lower() + value[2:] if re.match(r"^[A-Za-z]:/", value) else value


class RecoveryGuards(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.backup = self.root / "backup"
        self.backup.mkdir()
        self.guards = self.root / "guards"
        self.guards.mkdir()
        self.scratch = self.root / "scratch"
        self.scratch.mkdir()
        self.docs = list(yaml.safe_load_all((ROOT / "deploy/k8s/production/backup.yaml").read_text(encoding="utf-8")))
        self.docs.extend(yaml.safe_load_all((ROOT / "deploy/k8s/production/redis-backup-scripts.yaml").read_text(encoding="utf-8")))
        for item in self.docs:
            if item.get("kind") == "ConfigMap" and item["metadata"]["name"] == "cordum-redis-backup-scripts":
                for name, data in item["data"].items():
                    (self.guards / name).write_text(data.replace("/etc/cordum/backup", shellpath(self.guards)), encoding="utf-8", newline="\n")
        self.env = dict(os.environ, MOCK_ROOT=shellpath(self.root), REDIS_PASSWORD="test-password", REDISCLI_AUTH="test-password")
        self.write_topology()

    def write_topology(self, duplicate=False, epoch=1):
        for i in range(6):
            slots = ["0-5460", "5461-10922", "10923-16383"][i % 3]
            if duplicate and i == 1:
                slots = "0-5460"
            role = "master" if i < 3 else "slave"
            text = f"{i + 1:040x} host:6379@16379 myself,{role} - 0 0 {epoch} connected"
            (self.root / f"node{i}").write_text(text + (" " + slots if i < 3 else "") + "\n")

    def run_shell(self, script, extra="", **env):
        # Rewrite the source's scratch paths before injecting paths under /tmp.
        script = script.replace("/tmp/", shellpath(self.scratch) + "/").replace("/etc/cordum/backup", shellpath(self.guards)).replace("/backup", shellpath(self.backup))
        fake = r'''
redis-cli() {
  host=cordum-redis-0
  previous=
  for arg in "$@"; do
    [ "$previous" != -h ] || host=$arg
    if [ "$previous" = --rdb ]; then
      printf 'snapshot\n' > "$arg"
      [ "${FAILOVER_ON_BACKUP:-}" != 1 ] || sed -i 's/ 1 connected/ 2 connected/' "$MOCK_ROOT/node0"
      return 0
    fi
    previous=$arg
  done
  node=${host#cordum-redis-}; node=${node%%.*}
  case "$*" in
    *"CLUSTER NODES"*) cat "$MOCK_ROOT/node$node" ;;
    *"INFO replication"*) if [ "$node" -lt 3 ]; then echo role:master; else echo role:slave; fi ;;
    *FLUSHALL*) touch "$MOCK_ROOT/flushed" ;;
    *PING*) echo PONG ;;
    *DBSIZE*) echo 0 ;;
    *"INFO keyspace"*) echo db0:keys=0,expires=0,avg_ttl=0 ;;
    *"--cluster import"*)
      [ "${FAILOVER_ON_IMPORT:-}" != 1 ] || sed -i 's/ 1 connected/ 2 connected/' "$MOCK_ROOT/node0" ;;
  esac
}
redis-check-rdb() { return 0; }
redis-server() { return 0; }
env() { if [ "$1" = -u ]; then shift 2; fi; "$@"; }
timeout() { shift; "$@"; }
date() { echo 20261116T120000Z; }
'''
        return subprocess.run([BASH, "-c", fake + extra + "\n" + script], env={**self.env, **env}, capture_output=True, text=True, encoding="utf-8", timeout=20)

    def backup_script(self):
        job = next(x for x in self.docs if x["metadata"]["name"] == "cordum-redis-backup")
        return job["spec"]["jobTemplate"]["spec"]["template"]["spec"]["containers"][0]["command"][-1]

    def test_duplicate_shards_cannot_complete_backup(self):
        self.write_topology(duplicate=True)
        result = self.run_shell(self.backup_script())
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(list(self.backup.glob("*.sha256")))

    def test_complete_backup_binds_all_shards_to_checksums(self):
        result = self.run_shell(self.backup_script())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        manifests = list(self.backup.glob("*.sha256"))
        self.assertEqual(len(manifests), 1)
        self.assertEqual(len(manifests[0].read_text().splitlines()), 4)

    def test_failover_during_snapshot_cannot_complete_backup(self):
        result = self.run_shell(self.backup_script(), FAILOVER_ON_BACKUP="1")
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(list(self.backup.glob("*.sha256")))

    def create_backup(self):
        result = self.run_shell(self.backup_script())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return hashlib.sha256((self.backup / "redis-20261116T120000Z.sha256").read_bytes()).hexdigest()

    def preflush_script(self):
        doc = (ROOT / "docs/redis-operations.md").read_text(encoding="utf-8")
        return next(x for x in re.findall(r"sh -c '(.*?)'", doc, re.S) if "FLUSHALL" in x)

    def test_verified_backup_allows_flush(self):
        digest = self.create_backup()
        result = self.run_shell(self.preflush_script(), ts="20261116T120000Z", redis_manifest_sha256=digest)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((self.root / "flushed").exists())

    def test_corrupt_nonempty_rdb_cannot_flush(self):
        digest = self.create_backup()
        (self.backup / "redis-20261116T120000Z-node0.rdb").write_text("corrupt")
        result = self.run_shell(self.preflush_script(), ts="20261116T120000Z", redis_manifest_sha256=digest)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.root / "flushed").exists())

    def test_failover_invalidates_import_even_when_counts_match(self):
        digest = self.create_backup()
        preflight = self.run_shell(self.preflush_script(), ts="20261116T120000Z", redis_manifest_sha256=digest)
        self.assertEqual(preflight.returncode, 0, preflight.stdout + preflight.stderr)
        doc = (ROOT / "docs/redis-operations.md").read_text(encoding="utf-8")
        script = next(x for x in re.findall(r"sh -c '(.*?)'", doc, re.S) if "--cluster import" in x)
        stable = self.run_shell(script, ts="20261116T120000Z")
        self.assertEqual(stable.returncode, 0, stable.stdout + stable.stderr)
        changed = self.run_shell(script, ts="20261116T120000Z", FAILOVER_ON_IMPORT="1")
        self.assertNotEqual(changed.returncode, 0, changed.stdout + changed.stderr)
        self.assertIn("imported", changed.stdout)

    def test_failover_between_flush_and_import_is_rejected(self):
        digest = self.create_backup()
        preflight = self.run_shell(self.preflush_script(), ts="20261116T120000Z", redis_manifest_sha256=digest)
        self.assertEqual(preflight.returncode, 0, preflight.stdout + preflight.stderr)
        self.write_topology(epoch=2)
        doc = (ROOT / "docs/redis-operations.md").read_text(encoding="utf-8")
        script = next(x for x in re.findall(r"sh -c '(.*?)'", doc, re.S) if "--cluster import" in x)
        changed = self.run_shell(script, ts="20261116T120000Z")
        self.assertNotEqual(changed.returncode, 0, changed.stdout + changed.stderr)
        self.assertNotIn("imported", changed.stdout)

    def test_incompatible_passwords_rejected_before_flush(self):
        digest = self.create_backup()
        script = self.preflush_script()
        for password in ("percent%%password", "two words"):
            with self.subTest(password=password):
                result = self.run_shell(script, REDISCLI_AUTH=password, ts="20261116T120000Z", redis_manifest_sha256=digest)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertFalse((self.root / "flushed").exists())

    def nats_fixture(self):
        snapshot = self.backup / "nats-test"
        snapshot.mkdir()
        checksums = []
        for stream in ("CORDUM_SYS", "CORDUM_JOBS"):
            (snapshot / (stream + ".snapshot")).mkdir()
            for name in ("backup.json", "stream.tar.s2"):
                relative = f"{stream}.snapshot/{name}"
                data = b"snapshot-content"
                (snapshot / relative).write_bytes(data)
                # MSYS sha256sum defaults to binary mode; Linux defaults to text.
                mode = " *" if os.name == "nt" else "  "
                checksums.append(hashlib.sha256(data).hexdigest() + mode + relative + "\n")
        manifest = snapshot / "SHA256SUMS"
        manifest.write_text("".join(checksums), encoding="utf-8", newline="\n")
        digest = hashlib.sha256(manifest.read_bytes()).hexdigest()
        doc = (ROOT / "docs/production.md").read_text(encoding="utf-8")
        script = next(x for x in re.findall(r"sh -c '(.*?)'", doc, re.S) if "nats stream restore" in x)
        return snapshot, digest, script

    def test_nats_checks_both_snapshots_before_removing_either_stream(self):
        snapshot, digest, script = self.nats_fixture()
        fake = 'nats() { case "$*" in *"stream rm"*) touch "$MOCK_ROOT/removed" ;; esac; }'
        (snapshot / "CORDUM_JOBS.snapshot/stream.tar.s2").write_bytes(b"nonempty-corruption")
        result = self.run_shell(script, fake, nats_ts="test", nats_manifest_sha256=digest)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.root / "removed").exists())

    def test_nats_trusted_manifest_accepts_intact_snapshots(self):
        _, digest, script = self.nats_fixture()
        fake = 'nats() { case "$*" in *"stream rm"*) touch "$MOCK_ROOT/removed" ;; esac; }'
        result = self.run_shell(script, fake, nats_ts="test", nats_manifest_sha256=digest)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((self.root / "removed").exists())

    def test_nats_cannot_trust_replaced_manifest(self):
        snapshot, digest, script = self.nats_fixture()
        fake = 'nats() { case "$*" in *"stream rm"*) touch "$MOCK_ROOT/removed" ;; esac; }'
        manifest = snapshot / "SHA256SUMS"
        manifest.write_bytes(manifest.read_bytes() + b"extra-entry\n")
        result = self.run_shell(script, fake, nats_ts="test", nats_manifest_sha256=digest)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.root / "removed").exists())


if __name__ == "__main__":
    unittest.main()
