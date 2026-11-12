# ADR-012: Runner Isolation Stack

- Status: Proposed
- Date: 2026-11-12
- Scope: M3-01, managed Linux runner decision only

## Context

Permission to build or test must not confer unrelated execution authority.
The roadmap's M3 work therefore needs a boundary for arbitrary code, not an
argument classifier presented as confinement. The initial target is a managed
Linux execution plane; WSL2 Ubuntu-24.04 is development-only, not qualification.

At source baseline `549772371995b9eb5a95a2c6c8685551e0231724`, Cordum has no
managed arbitrary-code sandbox. [safeexec](../../core/edge/safeexec/safeexec.go)
wraps Edge's own subprocesses; it does not install an isolation runtime.
[SandboxProfile](../../core/infra/config/safety_policy.go) names `Isolated`,
`NetworkAllowlist`, `FsReadOnly` and `FsReadWrite` constraints. The
[scheduler](../../core/controlplane/scheduler/engine.go) serializes them into
`CORDUM_POLICY_CONSTRAINTS`; that transport is not runtime confinement.
Helm's control-plane seccomp settings do not confine separately executed jobs.

This document proposes a stack and acceptance obligations. It implements
nothing and makes no supported, enforced, verified or qualified capability
claim. All mechanisms below describe intended composition, not measured Cordum
behavior. M3-02 through M3-12 own implementation, negative-effect evidence and
qualification; documentation checks are not execution evidence.

## Decision

- Choose S1: one gVisor `runsc` sandbox per run, using systrap, plus independent
  host resource, filesystem and network restrictions.
- Propose dedicated x86_64 Ubuntu 24.04 GA hosts, Linux 6.8 floor, unified cgroup
  v2. No host is qualified by this choice.
- Use a dedicated execution domain, separate from the management plane and
  protected applications; the workload receives no production credentials.
- Map the six escape classes to explicit enforcement points and owning rows.
  Q7's guest-process ceiling remains a release blocker, not assumed coverage.
- Assemble existing mechanisms only: no new sandbox, custom kernel, driver,
  kernel module or BPF LSM. No silent fallback to runc or host execution.

## Stack comparison

S1 combines gVisor/systrap with host cgroup v2, per-run network namespaces and
trusted-side default-deny routing, private read-only inputs, a bounded writable
workspace, and a non-root workload with no capabilities or privilege escalation.
S2 combines runc, an explicit syscall allowlist, Landlock filesystem and ABI4
TCP restrictions, cgroup v2 and namespaces. S3 is a conventional KVM microVM
stack such as Kata Containers or Firecracker, not an alternative non-KVM service.
Each cell states a proposed mechanism and a limitation; none is a PASS result.

| Requirement | S1 gVisor | S2 runc + seccomp + Landlock | S3 microVM |
| --- | --- | --- | --- |
| Q1 | Non-root guest UID/GID, empty capability sets, no-new-privileges; trusted runsc helpers need separate host privileges. | Non-root, empty capabilities and no-new-privileges; shares the host kernel through allowed syscalls. | Non-root guest and constrained VMM identity; a VM does not remove guest privilege by itself. |
| Q2 | Digest-checked private copies mounted read-only, overlays disabled; a host-writable shared lower file is not immutable. | Same private copies and read-only mounts plus Landlock; Landlock does not freeze another writer's bytes. | Read-only private disk/share inputs; guest read-only flags cannot freeze an externally mutable backing file. |
| Q3 | One size/inode-limited tmpfs workspace; every writable scratch mount shares its budget, no writable root overlay. | Bounded workspace plus Landlock allowlist; not a resource quota without separate limits. | Bounded guest scratch disk and host backing quota; guest quotas alone cannot bound VMM resource use. |
| Q4 | No management socket mounts, host-uds=none, host-fifo=none; runtime control sockets must remain inaccessible too. | No host sockets mounted or inherited; Landlock ABI4 alone does not cover all IPC sockets. | No management socket/device passthrough; VM orchestration APIs remain trusted and external. |
| Q5 | Private PID/IPC views; workload descriptor allowlist limited to intended stdio; runtime-internal gofer channels are not workload FDs. | Close inherited descriptors and isolate IPC/PID namespaces; seccomp cannot revoke an already usable descriptor. | Private guest IPC and explicit virtual devices; inherited VMM descriptors and shared channels need host review. |
| Q6 | Netstack plus trusted routed-veth filtering; only broker proposals and explicit mirrors allowed. Netstack alone is not an egress policy. | Host default-deny routing plus Landlock TCP-port rules; ABI4 misses UDP and is not an address allowlist. | Host TAP/veth filtering and broker-only authority; VM isolation alone allows network effects. |
| Q7 | Host cpu.max, memory.max and pids.max plus whole-sandbox teardown; guest task ceiling is unresolved and must block release until proven. | Host cgroup task/resource limits and subtree teardown; no isolation from kernel bugs or instantaneous kill guarantee. | Host VMM limits plus guest task controls and VM destruction; host pids.max does not count guest processes. |
| Q8 | Trusted launcher confines helpers before create, installs spec/mounts/rules before start; runtime defaults or post-start attachment are insufficient. | Trusted setup installs namespaces, cgroups, Landlock and seccomp before exec; a later supervisor cannot repair an initial gap. | Prepare host envelope and VM devices before guest code; guest boot/setup remains trusted until restrictions are established. |
| Q9 | Per-run capability probe and pinned profile; refuse any missing item, including an unproven guest task cap. | Probe required ABI/rights/controllers and deny missing coverage; do not use best-effort Landlock downgrade. | Probe KVM, device/network policy and guest controls; no hardware or runtime substitution without requalification. |

Chosen: S1

The roadmap identifies gVisor or another qualified runtime as the separation
layer for hostile code. [Systrap](https://gvisor.dev/docs/architecture_guide/platforms/)
intercepts syscalls using seccomp traps without requiring nested virtualization.
S2 still exposes host syscall implementations behind a filter; Landlock's
coverage varies with the ABI. S3 adds a guest kernel/VMM and KVM availability
requirement, so it remains a higher-assurance candidate to evaluate, not a claim
that every microVM is safer. See the [gVisor security model](https://gvisor.dev/docs/architecture_guide/security/)
and [Firecracker KVM requirements](https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md).

S1 is conditional on closing Q7 before admission: host PID accounting must not
be substituted for a guest task bound. A source-level controller is not proof
of a correctly configured, workload-inaccessible ceiling. M3-07 must demonstrate
that property using the pinned runtime; otherwise M3-03 refuses the profile and
the stack decision must be revisited rather than silently weakened.
