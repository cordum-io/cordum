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

## Reference host

Kernel version floor: Linux 6.8 (proposed reference series; not yet qualified).
cgroup version: v2 (unified hierarchy only; hybrid or v1 hosts are refused)

Architecture: x86_64 initially. Distribution: Ubuntu 24.04 LTS, GA kernel series
with security updates; its [release notes](https://discourse.ubuntu.com/t/noble-numbat-release-notes/39890)
identify Linux 6.8. Ubuntu's [unified-mode default dates to 21.10](https://discourse.ubuntu.com/t/impish-indri-release-notes/21951),
but a default is not evidence of a host's boot configuration or delegation.
Runtime: direct OCI `runsc` on systrap, not Docker or a shared containerd daemon.
Upstream [installation requirements](https://gvisor.dev/docs/user_guide/install/)
currently list Linux 5.6+ and x86_64/ARM64; they do not qualify a Cordum profile.

M3-12 must pin the exact patched kernel build/configuration, distribution image,
runsc release and binary SHA-256, OCI profile, helper binaries and nftables
version in each qualification record. No floating `latest` is admissible.
The 6.8 floor selects the series to qualify, not support for older kernels or
blanket qualification of later versions. Only the exact recorded host/profile
can inherit its own evidence; upgrades or changed execution paths need re-tests.

### Required kernel features

The minima below are upstream feature introduction points, not an alternative
host support matrix. The actual floor remains 6.8, with all features available
to the runner service. A kernel version string cannot replace a capability test.

| Feature | Minimum kernel | Used for | Source |
| --- | --- | --- | --- |
| seccomp filter / SECCOMP_RET_TRAP on x86_64 | 3.5 | Systrap interception and host syscall reduction | [seccomp(2)](https://man7.org/linux/man-pages/man2/seccomp.2.html) |
| no_new_privs | 3.5 | Prevent workload privilege gain on exec | [Kernel API](https://docs.kernel.org/userspace-api/no_new_privs.html) |
| Unified cgroup v2, memory.max, pids.max | 4.5 | Host memory/task envelope, including helpers | [cgroups(7)](https://man7.org/linux/man-pages/man7/cgroups.7.html) |
| cgroup v2 CPU controller / cpu.max | 4.15 | Host CPU quota | [cgroups(7)](https://man7.org/linux/man-pages/man7/cgroups.7.html) |
| memory.oom.group | 4.19 | Group OOM treatment, not proof of teardown completion | [Linux 4.19 documentation](https://github.com/torvalds/linux/blob/v4.19/Documentation/admin-guide/cgroup-v2.rst) |
| cgroup.kill | 5.14 | Terminate dedicated run subtree, then wait for empty | [Linux 5.14 documentation](https://github.com/torvalds/linux/blob/v5.14/Documentation/admin-guide/cgroup-v2.rst) |
| Network namespaces plus nftables | 2.6.29 / 3.13 | Private link and trusted-side default-deny packet filtering | [clone(2)](https://man7.org/linux/man-pages/man2/clone.2.html), [nftables](https://wiki.nftables.org/wiki-nftables/index.php/What_is_nftables%3F) |
| User namespaces with UID/GID mappings | 3.8 | runsc namespace setup; actual service/LSM permission must pass probe | [user_namespaces(7)](https://man7.org/linux/man-pages/man7/user_namespaces.7.html) |
| tmpfs with size and inode limits | 2.4 | Bounded workspace; finite memory/swap policy also required | [tmpfs(5)](https://man7.org/linux/man-pages/man5/tmpfs.5.html) |
| openat2, RESOLVE_BENEATH / RESOLVE_NO_SYMLINKS | 5.6 | Host-side descriptor-based input materialization | [openat2(2)](https://man7.org/linux/man-pages/man2/openat2.2.html) |
| clone3 CLONE_INTO_CGROUP (or pre-confined trusted launcher) | 5.7 for this flag | Enter resource envelope before untrusted execution, never after | [clone(2)](https://man7.org/linux/man-pages/man2/clone.2.html) |

Require private mount, PID and IPC namespaces as part of the pinned OCI profile;
no host namespace sharing. CPU, memory and pids controllers must be enabled and
delegated, with finite configured limits and a swap policy. The trusted launcher
and all runsc/gofer helpers must start within a per-run ancestor envelope, so
runtime creation and teardown are bounded too. The workload gets no cgroup
control files or control API. Host pids.max bounds host tasks, not necessarily
virtual guest tasks. No io controller or host OverlayFS is relied on for this
initial tmpfs design. [Cgroup semantics](https://docs.kernel.org/admin-guide/cgroup-v2.html)
and [runsc v2 source](https://github.com/google/gvisor/blob/2a653cea7b2054a2dc9c6419337d3de8031275d6/runsc/cgroup/cgroup_v2.go)
are design inputs, not an acceptance result.

## Placement

Choose dedicated Linux runner hosts in an execution-only domain, separated from
management services and protected application namespaces. Do not place management
or production-effect credentials on these hosts. A narrowly scoped runner/proposal
identity is not a production credential; brokers must independently authorize
any proposed effect (M2B). The service must not expose runtime control to workloads.

M3-04's trusted runner service prepares an OCI bundle per run and invokes runsc
directly. Before untrusted start it must establish the resource envelope, private
namespaces, non-root workload credentials, empty capability sets, no-new-privileges,
mount/descriptor restrictions and egress policy. No post-start repair window.
Choose `--directfs=false`, `--host-uds=none`, `--host-fifo=none`,
`--overlay2=none` explicitly; restrict workload-influenced runtime annotations.
The gofer remains trusted filesystem mediation. Use a read-only root and private
read-only digest-checked input copies; writable temporary paths belong to the one
bounded workspace. A writable overlay over an input is not an immutable input.
See [filesystem modes](https://gvisor.dev/docs/user_guide/filesystem/).

For networked profiles use `--network=sandbox`, one run netns and a routed veth
pair. Install default-deny rules on the trusted host-side receive/forward path
before bringing up the link or starting code: INPUT denies host services and
FORWARD permits only the broker proposal entry and explicitly listed mirrors.
No alternative interface, bridge forwarding or host-network mode is allowed.
This placement is deliberate: gVisor emits link-layer packets via AF_PACKET;
run-netns OUTPUT rules alone would not cover them. This is a design inference
from [gVisor networking](https://gvisor.dev/docs/user_guide/networking/) and
[packet(7)](https://man7.org/linux/man-pages/man7/packet.7.html), to be measured
by M3-06/M3-10, not an existing Cordum guarantee.

Deny all unlisted protocols and destinations, including UDP, direct DNS,
cloud metadata, link-local, host loopback and IPv6 routes. If IPv6 is enabled,
it needs equivalent default-deny rules, not an IPv4-only policy. Guest loopback
is private, not a host-service route. Resolve any allowed endpoint in the trusted
plane and pin its address/port set; no wildcard internet/DNS escape. An allowed
mirror/proposal service must not act as a general proxy. If no destination is
allowed, use `--network=none` with no external link. M3-06 owns mirror policy.

A [Kubernetes RuntimeClass](https://kubernetes.io/docs/concepts/containers/runtime-class/)
with the [runsc containerd shim](https://gvisor.dev/docs/user_guide/containerd/quick_start/)
is a later packaging option, not the initial path. It would need
[Restricted Pod Security](https://kubernetes.io/docs/concepts/security/pod-security-standards/),
[`automountServiceAccountToken: false`](https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/)
and an execution-only placement policy. [NetworkPolicy](https://kubernetes.io/docs/concepts/services-networking/network-policies/)
depends on the plugin and policy handling; a pod can start before a newly created
policy is handled. RuntimeClass selection alone is not equivalence evidence.
M3-12/M6 must re-qualify that path; M3-10's kind harness does not qualify a host.

## Escape coverage

These are planned stopping mechanisms, not demonstrated prevention. M3-02 owns
the corpus; M3-12 must show the prohibited effect is absent, not merely an error.

| Class | Escape class | Stopping mechanism (S1) | Enforcement point | Corpus class / owning row |
| --- | --- | --- | --- | --- |
| X1 | Child processes | Same sandbox/mount/network boundary for all descendants, not an argv allowlist; guest count remains Q7-blocked. | Sentry plus trusted host envelope, outside workload control | Descendant confinement / M3-02, M3-04, M3-07 |
| X2 | Alternate interpreters | Interpreter changes do not change the sandbox boundary or grant host credentials. | Sentry and pre-start OCI profile | Interpreter-independent restrictions / M3-02, M3-04, M3-08 |
| X3 | Background jobs (nohup, setsid, double fork) | Destroy entire sandbox on end/timeout, terminate run cgroup including helpers, wait for empty; guest task limit separately unresolved. | Trusted supervisor and host cgroup ancestor, not parent PID tracking | No survivors/effects after termination / M3-02, M3-07 |
| X4 | Mutable inputs | Materialize independent bytes by content digest, verify final private copy, mount read-only with no writable alias/overlay. | Trusted materializer before start, then sandbox mounts | Mutation leaves consumed bytes unchanged / M2A-07, M3-05 |
| X5 | Path races | Resolve beneath runner-owned directories without symlinks, copy via held descriptors, verify copy; never re-resolve a host input path after verification. | Host openat2/descriptor boundary, not a lexical path check | Symlink/hard-link races cannot redirect reads or writes / M3-02, M3-05 |
| X6 | Direct network attempts | Netstack plus pre-installed trusted-side default-deny routing; no direct production credentials or alternate interface. | Host veth receive/forward hooks outside sandbox; broker authority boundary | No forbidden packets/API effects, IPv4 and IPv6 / M3-02, M3-06, M3-10 |

Hard links must not retain a shared mutable input inode: copy bytes into private
storage and verify that copy, rather than bind-mounting a verified path. Failure
to prove empty teardown quarantines the run/host; it cannot be reported as clean
completion or reused while survivors may remain. [Sandbox destruction source](https://github.com/google/gvisor/blob/2a653cea7b2054a2dc9c6419337d3de8031275d6/runsc/sandbox/sandbox.go)
is a mechanism reference, not proof of those lifecycle obligations.

## Mechanism limits

- **gVisor:** Sentry or reachable host-kernel vulnerabilities remain possible.
  Systrap depends on seccomp; compatibility failures must refuse the workload,
  not select a weaker runtime. Host resource limits do not imply guest counts.
- **seccomp:** Seccomp alone is not a sandbox. It filters syscall numbers and
  argument values, not the meaning of operations on permitted objects. See the
  [kernel's stated limits](https://docs.kernel.org/userspace-api/seccomp_filter.html).
  runsc's host filters and guest OCI filters are different layers; the latter
  require explicit `--oci-seccomp=true` (default false in the
  [researched source](https://github.com/google/gvisor/blob/2a653cea7b2054a2dc9c6419337d3de8031275d6/runsc/config/flags.go)).
  M3-08 owns the compatible syscall policy and proof it applies inside S1.
- **runc:** a container runtime is not a supplied strict profile. The
  [generated OCI example](https://github.com/opencontainers/runc/blob/d75aa704733ca73da71ca2d2d52bde534b0096c8/libcontainer/specconv/example.go)
  sets no-new-privileges but supplies no seccomp policy; callers must configure it.
- **Landlock:** only the selected ABI's handled rights are restricted. ABI1
  filesystem rights arrived in 5.13, ABI2 REFER in 5.19, ABI3 TRUNCATE in 6.2,
  ABI4 TCP bind/connect in 6.7, ABI5 device ioctl in 6.10 and ABI6 abstract-socket
  /signal scoping in 6.12 ([version table](https://man7.org/linux/man-pages/man7/landlock.7.html)).
  At the [6.8 reference ABI4](https://docs.kernel.org/6.8/userspace-api/landlock.html),
  TCP rules concern ports, not destination IPs; UDP and later rights are absent.
  This is not a claim about every newer ABI. S1 does not rely on guest Landlock.
- **cgroups / namespaces:** resource limits and separated views respectively;
  neither alone is an arbitrary-code security boundary. Teardown is observed,
  not inferred from a successful signal request.
- **nftables / NetworkPolicy:** correctness depends on actual packet paths and
  complete rules; NetworkPolicy also depends on plugin and initialization timing.
- **read-only mounts / tmpfs:** private read-only bytes do not establish safe
  semantics; tmpfs quotas do not validate content or prevent output exfiltration.
- **microVMs:** guest kernels, VMMs, devices and shared files remain trusted
  attack surfaces; hardware separation does not supply authorization policy.

## Residual risks

- Runtime/host-kernel defects and hardware side channels remain; host firmware
  and patching are part of the boundary, not eliminated by a user-space kernel.
- Work can deny service within permitted limits or through shared host pressure;
  bounded resources do not promise availability, and blocked teardown needs quarantine.
- Intentionally allowed APIs, including broker proposal submission, can be
  misused. Broker policy/credentials and M2B resource authorization remain separate.
- Allowed outputs can carry secrets or hostile content. M3-09 output scanning
  is planned defense, not a complete exfiltration guarantee.
- A compromised runner host, service or administrator defeats this trust model;
  hostile host administration is outside the boundary, not an adversary it defeats.
- Image, runtime, kernel and helper supply chains require PR2 qualification.
  Capability/profile drift between probe and start requires M3-03 rechecking.
- **Unresolved guest task ceiling:** researched [guest controller source](https://github.com/google/gvisor/blob/2a653cea7b2054a2dc9c6419337d3de8031275d6/pkg/sentry/fsimpl/cgroupfs/pids.go)
  has an unlimited initial setting. Trusted pre-start configuration and inability
  of the workload to raise it are not established here. Host pids.max is not a
  substitute. Q7 and admission stay blocked until M3-07 proves the guest ceiling.
- No runtime, syscall policy, network path or teardown behavior was executed for
  this ADR. X1-X6 remain unmeasured pending the corpus and reference-host tests.

## Capability refusal

Before every run, M3-03 must check the actual service identity/environment:

- Exact qualified kernel/profile identity, floor, namespace access and usable
  systrap; expected runsc release/digest and explicit flags, with no overrides.
- Unified cgroup v2, delegated CPU/memory/pids controllers, finite limits,
  memory/swap policy, group OOM handling and a working whole-subtree teardown.
- A proven guest task ceiling outside workload control, not host PID inference.
- Private immutable digest-checked inputs, safe materialization, no writable
  aliases/overlays, and workspace byte/inode quotas covering every writable path.
- No workload host/control sockets, leaked descriptors, shared host IPC,
  capabilities, elevated UID or path to production credentials.
- Default-deny rules on the trusted routed peer, including IPv6 and host-service
  denial, installed before start; or network=none with no external interface.
- The required guest syscall restrictions actually applied under the pinned
  runtime, not merely a seccomp field accepted in an OCI document (M3-08).

Any absent, stale, unknown or unproven required item must produce a typed refusal
before untrusted code starts. There is no weaker-run fallback. `SandboxProfile`
fields must map explicitly to compiled isolation, network and mount restrictions;
unenforced or unknown constraint kinds deny, and `Isolated=false` cannot bypass
this mandatory managed-runner floor. Probe success alone cannot qualify a release.

## Consequences

The proposed boundary follows arbitrary descendants rather than guessing command
semantics and separates execution from effect authority. Costs include gVisor
compatibility/performance limits, a separately operated host fleet and additional
failure/refusal paths. Performance is not measured. Qualification is Linux-only;
Windows/macOS clients may eventually submit to the managed plane, while native
endpoint enforcement remains M6-14. This ADR does not ship either client path.

## Open questions

| Question | Owner / acceptance obligation |
| --- | --- |
| Guest task ceiling and complete teardown | M3-07 / M3-03: trusted immutable limit and no surviving effects, or refuse S1. |
| Kubernetes RuntimeClass packaging | M3-12 / M6: re-qualify the distinct launch/network path. |
| KVM platform or microVM alternative | M6: assess assurance/operational tradeoffs, no automatic substitution. |
| ARM64 hosts | M6: independent architecture and platform qualification. |
| Tenant co-residency | PR2-10 / M6: sharing, side-channel and resource-boundary evidence. |
| Performance budgets | PR2-09: measure compatibility and overhead on pinned workloads/hosts. |
| Exact syscall deny policy and layering | M3-08: guest OCI seccomp opt-in and effective restrictions under runsc. |
| Package mirrors and network policy | M3-06: bounded endpoints, no proxy escape, on-wire negative-effect evidence. |
