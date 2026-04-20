# Cordum Governance

## Project Overview

Cordum is a deterministic control plane for autonomous AI agents with governance built in. This document outlines how the project is governed, how decisions are made, and how contributors can participate.

## Protocol Stability Pledge

Cordum commits to **wire-format stability** for enterprise adopters:

- **CAP v2 protobuf schemas** (`BusPacket`, `JobRequest`, `JobResult`, `Heartbeat`) are **frozen until February 2027**. No breaking changes to field numbers, field types, or required fields. New fields may be added as `optional`.
- **Safety Kernel evaluation API** (`POST /api/v1/policy/evaluate` request/response shape) is frozen for the same period.
- **Gateway REST API** endpoints documented in the [API reference](https://docs.cordum.io/api) will not have breaking changes within a major version.

### Deprecation Policy

Any deprecated API receives **2 minor versions** of deprecation warnings before removal:

1. **Minor N**: Feature marked deprecated in docs, logs emit warnings
2. **Minor N+1**: Deprecation warnings become errors in strict mode
3. **Minor N+2**: Feature removed

### Versioning

Cordum follows **Semantic Versioning** (semver):

| Bump | Meaning | Example |
|------|---------|---------|
| **Major** | Breaking API or wire-format changes | `1.0.0` → `2.0.0` |
| **Minor** | New features, backward-compatible | `1.0.0` → `1.1.0` |
| **Patch** | Bug fixes, security patches | `1.0.0` → `1.0.1` |

### Release Cadence

- **Patch releases**: Monthly (or immediately for critical security fixes)
- **Minor releases**: Quarterly
- **Major releases**: Annually, with 6-month migration window

## Project Structure


**Advisors:**

| Handle | Focus |
|--------|-------|
| *Open — accepting advisor applications* | Security, Protocol Design, Enterprise Integrations |

To apply as an advisor, open an issue with the `governance` label or email governance@cordum.io.

### Contributors

Anyone can contribute to Cordum. Contributors include:
- Code contributors (features, bug fixes)
- Documentation contributors
- Community managers (forums, Discord, issues)
- Security researchers

## Decision-Making Process

### Consensus Model

Cordum follows a **lazy consensus** model:

1. **Proposal:** Contributor proposes a change (PR, RFC, issue discussion)
2. **Review period:** Minimum 72 hours for community feedback
3. **Consensus:** If no objections from core maintainers → approved
4. **Escalation:** If disagreement → escalate to Project Lead

### Major Decisions

Major changes require explicit approval:

- **Architecture changes** (new components, protocols)
- **Breaking API changes** (major version bumps)
- **License changes** (requires unanimous consent)
- **Security policies** (vetted by security team)

**Process:**
1. Author submits RFC (Request for Comments) as GitHub issue
2. Community discussion (minimum 2 weeks)
3. Core team vote (majority required)
4. Project Lead final approval

### Day-to-Day Decisions

Minor changes (bug fixes, docs, small features) follow normal PR process:

1. Submit PR with clear description
2. Automated CI checks pass
3. 2+ approvals from maintainers
4. Merge

## Contribution Guidelines

See [CONTRIBUTING.md](./CONTRIBUTING.md) for detailed guidelines.

### Code Review Standards

All code must be reviewed:

- **1 approval:** Documentation, tests, small bug fixes
- **2 approvals:** New features, refactors
- **3 approvals:** Architecture changes, security-sensitive code

Reviewers check for:
- ✅ Correctness and test coverage
- ✅ Performance implications
- ✅ Security considerations
- ✅ Documentation updates
- ✅ Backward compatibility

## Roles and Responsibilities

### Maintainer

**Responsibilities:**
- Review and merge PRs
- Triage issues
- Participate in roadmap planning
- Mentor contributors

**How to become a maintainer:**
- Consistent, high-quality contributions over 6+ months
- Deep understanding of codebase
- Demonstrated good judgment
- Nominated by existing maintainer, approved by core team

### Area Owner

**Responsibilities:**
- Expert in a specific component (e.g., Safety Kernel)
- Final say on design decisions in their area
- Maintain documentation and roadmap for their area

**Areas:**
- Safety Kernel
- Workflow Engine
- Job Scheduler
- Pack System
- API Server
- Documentation

## Release Process

### Versioning

Cordum follows **Semantic Versioning** (semver):

- **Major (1.0.0):** Breaking API changes
- **Minor (0.1.0):** New features, backward-compatible
- **Patch (0.0.1):** Bug fixes, security patches

### Release Cadence

- **Major releases:** Annually or when needed
- **Minor releases:** Quarterly
- **Patch releases:** As needed (security: within 48h)

### Release Checklist

1. Update CHANGELOG.md
2. Run full test suite + benchmarks
3. Update documentation
4. Security scan (gosec, CodeQL)
5. Create release notes
6. Tag release in git
7. Build and publish artifacts
8. Announce on Discord, Twitter, mailing list

## Community Standards

### Code of Conduct

We follow the [Contributor Covenant Code of Conduct](./CODE_OF_CONDUCT.md).

**Enforcement:**
- **Level 1 (Warning):** Private message from maintainer
- **Level 2 (Temporary ban):** 30-day suspension from project
- **Level 3 (Permanent ban):** Removal from project

Reports: conduct@cordum.io

### Communication Channels

- **GitHub Issues:** Bug reports, feature requests
- **GitHub Discussions:** Design discussions, Q&A
- **Discord:** Real-time chat, community support
- **Mailing list:** Release announcements, major decisions
- **Twitter/X:** @cordum_io

## Conflict Resolution

If a dispute arises:

1. **Direct discussion:** Try to resolve with involved parties
2. **Maintainer mediation:** Request mediation from uninvolved maintainer
3. **Core team vote:** Escalate to core team
4. **Project Lead decision:** Final arbiter

## Roadmap and Planning

### Public Roadmap

The roadmap is maintained in [ROADMAP.md](./ROADMAP.md) and GitHub Projects.

**Priorities:**
1. Security and stability
2. Performance and scalability
3. New features
4. Developer experience

### Feature Requests

Submit feature requests as GitHub issues with:
- Use case and motivation
- Proposed design (if applicable)
- Willingness to contribute

Core team triages quarterly and adds to roadmap.

## Security

See [SECURITY.md](./SECURITY.md) for security policies.

**Key points:**
- Responsible disclosure required
- 90-day embargo before public disclosure
- Security patches released within 24-48h (critical)

## License

Cordum is licensed under the **Business Source License (BUSL-1.1)**.

**Key terms:**
- ✅ View, modify, use internally
- ❌ Offer as hosted service (requires commercial license)
- ❌ Resell or redistribute (requires commercial license)

See [LICENSE](./LICENSE) for full terms.

## Becoming a Committer

**Criteria:**
1. 20+ merged PRs over 6+ months
2. High-quality contributions (code, docs, reviews)
3. Good understanding of codebase
4. Active participation in community
5. Alignment with project values

**Process:**
1. Nominated by existing committer
2. Core team reviews contributions
3. Majority vote by core team
4. Formal invitation extended

## Project Evolution

This governance model may evolve as the project grows:

- **Current stage:** Early adopters, core team-driven
- **Next stage:** Foundation/working group (if community grows)
- **Future:** Possible transition to independent foundation

Changes to this document require approval from Project Lead.

---

**Last updated:** January 2026
**Next review:** June 2026

**Questions?** governance@cordum.io
